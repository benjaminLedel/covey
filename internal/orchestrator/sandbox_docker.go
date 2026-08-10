package orchestrator

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DockerProvider starts coveyd in a container — real isolation at the
// namespace level instead of the process level (spec/01: the sandbox is dumb
// and replaceable; only the home is persistent and gets mounted as a volume).
// It deliberately talks to the docker CLI instead of the SDK libs: no heavy
// dependency, and `docker run`/`docker stop` are the whole truth.
// The container inherits nothing from the host — the environment consists
// exclusively of what the control plane puts into SandboxSpec.Env.
type DockerProvider struct {
	// Image is the sandbox image (coveyd + runtime), see Dockerfile.sandbox.
	// It applies to agents that name none of their own.
	Image string
	// Profiles maps a profile name to its image (`base`, `dev` — spec/16).
	// An agent's value that is not a profile is taken as an image reference:
	// that is the "org-owned: anything" row of the profile table, and it needs
	// no second field to hold it.
	Profiles map[string]string
	// AgentImages reports which workplaces the agents are configured for
	// (value from the agent → number of agents). Used only by Check, to ask
	// about the images that are actually in use. nil = unknown; then only the
	// instance default is asked about.
	AgentImages func(ctx context.Context) (map[string]int, error)
	// DataDir holds the persistent agent homes on the host.
	DataDir string
	// DockerBin overrides the CLI path (default "docker") — for tests.
	DockerBin string
	// EgressProxyURL routes the container's outbound HTTP(S) traffic through the
	// Covey egress allowlist proxy (empty = no proxy). The value is the
	// container-side URL (usually http://host.docker.internal:<port>).
	// Cooperative ("proxy" mode): the control plane connection
	// (host.docker.internal) and loopback bypass the proxy via NO_PROXY.
	EgressProxyURL string
	// EgressIsolation: "proxy" (cooperative, default) or "network" (hard).
	// In network mode the sandbox runs on an internal Docker network without
	// internet; a proxy container is the only way out and enforces the
	// allowlist — no longer bypassable by the agent. The control plane WS then
	// runs through the proxy via HTTP CONNECT (requires TLS/wss, see docs).
	EgressIsolation string
	// EgressProxyImage is the proxy image for network mode (make egress-image).
	EgressProxyImage string
	// EgressProxyEnv are the proxy container's env variables that are the same
	// for every runner (COVEY_CONTROL_URL, COVEY_EGRESS_ALLOW incl. the control
	// plane host, COVEY_EGRESS_PROXY_ADDR). The runner token comes per runner
	// from EgressRunnerFor — deliberately NOT the database URL: the proxy is an
	// enforcement point, not a database client (spec/16, "Trust boundary").
	EgressProxyEnv map[string]string
	// EgressRunnerFor resolves an organisation's runner and its token. In hard
	// isolation mode each runner gets its own segment and its own proxy, so
	// this is what decides where a sandbox lands.
	EgressRunnerFor func(ctx context.Context, orgID uuid.UUID) (runnerID uuid.UUID, token string, err error)

	// proxyFresh notes the runners whose proxy container this process has
	// already renewed. See ensureEgressProxy for why that is necessary.
	proxyMu    sync.Mutex
	proxyFresh map[uuid.UUID]bool
}

// egressProxyAlias is the proxy's DNS name on its internal network. It may stay
// the same for every runner because the networks are separate — the name only
// has to be unambiguous within one segment.
const egressProxyAlias = "covey-egress"

// The internal network and the proxy container carry the runner they belong to
// in their name. Instance-wide singletons would put the sandboxes of every
// organisation into the same segment, and `--internal` only cuts the way out,
// not the way sideways — so two tenants' agents could reach each other
// directly, past every allowlist (spec/16, "Egress with distributed runners").
//
// The short form of the ID suffices: it is unambiguous on one host, and the
// full one makes container names that no longer fit in a terminal.
func egressNetworkFor(runnerID uuid.UUID) string {
	return "covey-egress-internal-" + short(runnerID)
}

func egressProxyNameFor(runnerID uuid.UUID) string {
	return "covey-egress-proxy-" + short(runnerID)
}

func short(id uuid.UUID) string { return id.String()[:8] }

// sandboxHome is the fixed home path inside the container (user `agent` in the image).
const sandboxHome = "/home/agent"

// sandboxUID/GID is the user `agent` from Dockerfile.sandbox. The home is
// chowned to it: if the control plane (running as root in its container)
// creates the directory, the sandbox user could otherwise not write into it.
const (
	sandboxUID = 1001
	sandboxGID = 1001
)

// imageFor resolves an agent's workplace. One rule, in this order: nothing
// named → the instance default; a known profile → its image; anything else →
// taken literally.
func (p *DockerProvider) imageFor(want string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		return p.Image
	}
	if img, ok := p.Profiles[want]; ok && img != "" {
		return img
	}
	return want
}

func (p *DockerProvider) docker() string {
	if p.DockerBin != "" {
		return p.DockerBin
	}
	return "docker"
}

// AgentHome satisfies FileAccess: the home lives as a directory on the host
// (`<DataDir>/homes/<agent-id>`) and is mounted into the sandbox — readable and
// writable even while no container is running.
func (p *DockerProvider) AgentHome(agentID uuid.UUID) (Home, error) {
	return Home{Path: p.homePath(agentID.String()), UID: sandboxUID, GID: sandboxGID}, nil
}

// homePath is the single place where a home's host path is formed.
func (p *DockerProvider) homePath(agentID string) string {
	home := filepath.Join(p.DataDir, "homes", agentID)
	if abs, err := filepath.Abs(home); err == nil {
		return abs
	}
	return home
}

func (p *DockerProvider) Start(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	home := spec.HomeDir
	if home == "" {
		home = p.homePath(spec.AgentID.String())
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	// 0o755, not 0o700: the chown below is best-effort (only succeeds as root —
	// the deployment container; locally the control plane runs as a normal host
	// user and Docker Desktop is left to map the ownership). When that mapping
	// does not happen — observed on Docker Desktop for Mac, bind mount owned by
	// root regardless of the chown call below — a 0o700 directory leaves the
	// sandbox user with no traverse permission on its OWN home. Go's os/exec
	// chdir()s into cmd.Dir (this path, via runtime_claudecode.go) as part of
	// starting the runtime; that chdir then fails EACCES, and Go's error
	// wrapping folds it into "fork/exec <runtime>: permission denied" — which
	// reads like the runtime binary itself is unrunnable, not like a directory
	// the agent cannot even enter. 0o755 keeps the directory traversable no
	// matter which uid ends up owning it; secrets never live here regardless
	// (spec/04), so the wider "other" read/traverse bit is not a new exposure.
	if err := os.MkdirAll(home, 0o755); err != nil {
		return nil, err
	}
	_ = os.Chown(home, sandboxUID, sandboxGID)

	name := containerName(spec.AgentID.String())
	// Clear leftovers of a crashed predecessor sandbox — the name has to be free.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()

	// --init: the runtime spawns child processes; tini as PID 1 inherits and
	// reaps them, while coveyd still gets SIGTERM passed through cleanly.
	args := []string{"run", "-d", "--rm", "--init",
		"--name", name,
		"-v", home + ":" + sandboxHome,
		"-e", "HOME=" + sandboxHome,
		"-e", "COVEY_HOME=" + sandboxHome,
	}

	switch p.EgressIsolation {
	case "network":
		// Hard isolation: sandbox only on the internal network (no internet). The
		// only way out is the proxy container; the control plane WS runs through
		// it via CONNECT too — hence NO host.docker.internal add-host here, but
		// the proxy as HTTP(S)_PROXY. Loopback (the daemon's action proxy) stays
		// direct.
		runnerID, runnerToken, err := p.egressRunner(ctx, spec.OrgID)
		if err != nil {
			return nil, fmt.Errorf("resolve egress runner: %w", err)
		}
		if err := p.ensureNetworkIsolation(ctx, runnerID, runnerToken); err != nil {
			return nil, fmt.Errorf("prepare egress network: %w", err)
		}
		proxyURL := egressURLWithCreds("http://"+egressProxyAlias+":8888", spec.AgentID.String(), spec.EgressToken)
		args = append(args, "--network", egressNetworkFor(runnerID))
		for _, e := range proxyEnvVars(proxyURL, "localhost,127.0.0.1,::1") {
			args = append(args, "-e", e)
		}
	default:
		// Cooperative or without egress: --add-host makes the control plane
		// reachable as host.docker.internal (Docker Desktop ships this).
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
		if p.EgressProxyURL != "" {
			// Outbound traffic through the allowlist proxy; control plane and
			// loopback stay direct (NO_PROXY), otherwise the daemon WS would be
			// routed through as well. The proxy URL carries the per-sandbox token
			// by which the proxy identifies the agent.
			proxyURL := egressURLWithCreds(p.EgressProxyURL, spec.AgentID.String(), spec.EgressToken)
			for _, e := range proxyEnvVars(proxyURL, "host.docker.internal,localhost,127.0.0.1,::1") {
				args = append(args, "-e", e)
			}
		}
	}

	for k, v := range spec.Env {
		if k == "COVEY_WS_URL" {
			v = rewriteLoopbackForDocker(v)
		}
		args = append(args, "-e", k+"="+v)
	}
	image := p.imageFor(spec.Image)
	args = append(args, image)

	out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "pull access denied") ||
			strings.Contains(msg, "Unable to find image") {
			return nil, fmt.Errorf("sandbox image %q is missing — build it with `%s`: %s",
				image, buildHint(image), msg)
		}
		return nil, fmt.Errorf("docker run: %v: %s", err, msg)
	}
	return &dockerSandbox{docker: p.docker(), name: name}, nil
}

type dockerSandbox struct {
	docker string
	name   string
}

func (s *dockerSandbox) Stop(ctx context.Context) error {
	// docker stop = SIGTERM, SIGKILL after the timeout; --rm removes the container.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(stopCtx, s.docker, "stop", "-t", "5", s.name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		// Force cleanup so the name is free for the next wake.
		_ = exec.CommandContext(stopCtx, s.docker, "rm", "-f", s.name).Run()
	}
	return nil
}

// Check reports what stands between this platform and a running sandbox —
// before an agent has to find out.
//
// The two things it asks about are the two ways a fresh installation fails, and
// both used to become visible only in the recording of a task that had already
// been waiting for its first run: no Docker daemon reachable (typically the
// control plane in a container without the socket mounted), and no sandbox
// image built. Neither is an error in the platform, and neither can be guessed
// from the log line the agent produces.
//
// Empty result = nothing in the way. Messages are written for whoever operates
// the instance and each one names its remedy.
func (p *DockerProvider) Check(ctx context.Context) []string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, p.docker(), "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		return []string{fmt.Sprintf(
			"no Docker daemon reachable — every wake fails at starting the sandbox. "+
				"Running the control plane in a container? Then it needs the host's socket: "+
				"`-v /var/run/docker.sock:/var/run/docker.sock`. (%s)",
			firstLine(string(out), err))}
	}

	var problems []string
	// Since the image hangs off the agent, "is the image there?" is no longer
	// one question but one per image in use. Asking about every configured
	// profile instead would warn every fresh installation about a dev image
	// nobody wants — the answer has to follow the agents, not the config.
	for image, agents := range p.imagesInUse(ctx) {
		out, err := exec.CommandContext(ctx, p.docker(), "image", "inspect", image).CombinedOutput()
		if err == nil {
			continue
		}
		// The number of agents only when it is known: "0 agents work in it"
		// would be a statement about the data source, not about the platform.
		who := ""
		if agents > 0 {
			who = fmt.Sprintf(", and %d agent(s) work in it", agents)
		}
		problems = append(problems, fmt.Sprintf(
			"sandbox image %q is missing%s — build it once: `%s` (%s)",
			image, who, buildHint(image), firstLine(string(out), err)))
	}
	sort.Strings(problems) // map iteration order must not shuffle the startup log
	if p.EgressIsolation == "network" && p.EgressProxyImage != "" {
		if out, err := exec.CommandContext(ctx, p.docker(), "image", "inspect", p.EgressProxyImage).CombinedOutput(); err != nil {
			problems = append(problems, fmt.Sprintf(
				"egress proxy image %q is missing, and hard isolation needs it — "+
					"build it once: `docker build -f Dockerfile.egress -t %s .` (%s)",
				p.EgressProxyImage, p.EgressProxyImage, firstLine(string(out), err)))
		}
	}
	return problems
}

// imagesInUse returns the resolved images the agents actually work in, with
// how many of them each one carries. Without a source of that information the
// instance default is the only thing that can be asked about — which is what
// the answer was before the image hung off the agent.
func (p *DockerProvider) imagesInUse(ctx context.Context) map[string]int {
	if p.AgentImages == nil {
		return map[string]int{p.Image: 0}
	}
	wanted, err := p.AgentImages(ctx)
	if err != nil || len(wanted) == 0 {
		return map[string]int{p.Image: 0}
	}
	out := map[string]int{}
	for want, n := range wanted {
		out[p.imageFor(want)] += n
	}
	return out
}

// buildHint names the make target that produces this image. A message that
// sends someone to `make sandbox-image` while the dev image is what is missing
// costs them the build twice — and the second one still does not help.
func buildHint(image string) string {
	if strings.Contains(image, "sandbox-dev") {
		return "make sandbox-image-dev"
	}
	return "make sandbox-image"
}

// firstLine keeps a CLI error to the one line that says something. Docker
// prints its usage help after some errors, and a wall of it in a log line or in
// the interface buries the sentence that matters.
//
// "[]" is skipped explicitly: `docker image inspect` writes the empty result
// list to stdout and its reason to stderr, so the first line of the two
// combined is a pair of brackets — technically the output, and useless to
// everyone reading it.
func firstLine(out string, fallback error) string {
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" && l != "[]" {
			return l
		}
	}
	return fallback.Error()
}

// containerName derives a stable, docker-compatible name from the agent ID —
// at most one sandbox exists per agent (serial, spec/03).
func containerName(agentID string) string {
	return "covey-sandbox-" + agentID
}

// egressURLWithCreds appends the per-sandbox token as proxy credentials to the
// proxy URL (agentID:token). The proxy identifies the agent from it. Without a
// token the URL stays unchanged (the proxy then answers fail-closed with 407).
func egressURLWithCreds(base, agentID, token string) string {
	if token == "" {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.User = url.UserPassword(agentID, token)
	return u.String()
}

// proxyEnvVars returns the HTTP(S)_PROXY/NO_PROXY variables (upper and lower
// case, because tools read sometimes one, sometimes the other).
func proxyEnvVars(proxyURL, noProxy string) []string {
	return []string{
		"HTTP_PROXY=" + proxyURL, "http_proxy=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL, "https_proxy=" + proxyURL,
		"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
	}
}

// egressRunner resolves the organisation's runner. Without a resolver the
// provider cannot say which segment a sandbox belongs in — and guessing would
// mean putting it into a foreign one, so it fails instead.
func (p *DockerProvider) egressRunner(ctx context.Context, orgID uuid.UUID) (uuid.UUID, string, error) {
	if p.EgressRunnerFor == nil {
		return uuid.Nil, "", fmt.Errorf("hard isolation without a runner resolver — the egress segment is unknown")
	}
	return p.EgressRunnerFor(ctx, orgID)
}

// ensureNetworkIsolation idempotently establishes the internal network and the
// running proxy container — the two building blocks of the hard egress mode,
// one set per runner.
func (p *DockerProvider) ensureNetworkIsolation(ctx context.Context, runnerID uuid.UUID, runnerToken string) error {
	if err := p.ensureEgressNetwork(ctx, runnerID); err != nil {
		return err
	}
	return p.ensureEgressProxy(ctx, runnerID, runnerToken)
}

// ensureEgressNetwork creates the internal network (no gateway to the outside).
func (p *DockerProvider) ensureEgressNetwork(ctx context.Context, runnerID uuid.UUID) error {
	name := egressNetworkFor(runnerID)
	if err := exec.CommandContext(ctx, p.docker(), "network", "inspect", name).Run(); err == nil {
		return nil // already exists
	}
	out, err := exec.CommandContext(ctx, p.docker(), "network", "create", "--internal", name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("create internal network: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureEgressProxy starts the proxy container if it is not running: on the
// internal network (alias covey-egress) as the only way out, plus the default
// bridge network for internet access.
//
// The first call per process renews the container even when it is running. The
// reason is the runner token: the built-in runner rolls a new one at every
// start of the control plane, and a proxy left over from the previous one would
// carry the old one in its environment. It would then get a 401 on its next
// allowlist fetch and — fail-closed, correctly — block everything. A container
// restart is cheap; the proxy holds no state.
func (p *DockerProvider) ensureEgressProxy(ctx context.Context, runnerID uuid.UUID, runnerToken string) error {
	name := egressProxyNameFor(runnerID)

	p.proxyMu.Lock()
	if p.proxyFresh == nil {
		p.proxyFresh = map[uuid.UUID]bool{}
	}
	fresh := p.proxyFresh[runnerID]
	p.proxyFresh[runnerID] = true
	p.proxyMu.Unlock()

	if fresh {
		// Already renewed in this process — from here on, running is enough.
		out, _ := exec.CommandContext(ctx, p.docker(), "inspect", "-f", "{{.State.Running}}", name).Output()
		if strings.TrimSpace(string(out)) == "true" {
			return nil
		}
	}
	// Clear leftovers so the name is free.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()

	image := p.EgressProxyImage
	if image == "" {
		image = "covey-egress:latest"
	}
	args := []string{"run", "-d", "--restart", "unless-stopped",
		"--name", name,
		"--network", egressNetworkFor(runnerID),
		"--network-alias", egressProxyAlias,
		"--add-host", "host.docker.internal:host-gateway",
	}
	for k, v := range p.EgressProxyEnv {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "-e", "COVEY_RUNNER_TOKEN="+runnerToken)
	args = append(args, image)
	if runOut, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(runOut))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "Unable to find image") {
			return fmt.Errorf("egress proxy image %q is missing — build it with `make egress-image`: %s", image, msg)
		}
		return fmt.Errorf("start egress proxy: %v: %s", err, msg)
	}
	// Internet side: attach the default bridge network so the proxy reaches the
	// outside (and the control plane via host-gateway).
	if connOut, err := exec.CommandContext(ctx, p.docker(), "network", "connect", "bridge", name).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(connOut))
		if !strings.Contains(msg, "already exists") && !strings.Contains(msg, "already connected") {
			return fmt.Errorf("attach egress proxy to the bridge network: %v: %s", err, msg)
		}
	}
	return nil
}

// rewriteLoopbackForDocker rewrites a loopback URL of the control plane to
// host.docker.internal: inside the container "localhost" points at the
// container itself, not at the host. Non-loopback URLs (a real hostname/IP in
// COVEY_PUBLIC_URL) are left untouched.
func rewriteLoopbackForDocker(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return rawURL
	}
	host := "host.docker.internal"
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	u.Host = host
	return u.String()
}
