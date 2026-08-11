package orchestrator

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	Image string
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
	// EgressProxyEnv are the proxy container's env variables (DB URL,
	// COVEY_EGRESS_ALLOW incl. control plane host, COVEY_EGRESS_PROXY_ADDR).
	EgressProxyEnv map[string]string
}

// Names of the building blocks of the hard isolation mode.
const (
	egressNetwork    = "covey-egress-internal" // internal network without internet
	egressProxyName  = "covey-egress-proxy"    // the proxy container
	egressProxyAlias = "covey-egress"          // DNS alias on the internal network
)

// DindImage is the Docker-in-Docker sidecar image (make dind-image is not a
// thing — this is a stock image pulled once and cached, unlike the
// covey-sandbox/covey-egress images that are project-specific builds).
const DindImage = "docker:27-dind"

// dindAlias is the DNS name the sandbox reaches its own Docker-in-Docker
// sidecar under (DOCKER_HOST) — an alias, not the container name, so it stays
// the same across the container's rm+recreate on every wake.
const dindAlias = "dind"

// sandboxHome is the fixed home path inside the container (user `agent` in the image).
const sandboxHome = "/home/agent"

// sandboxUID/GID is the user `agent` from Dockerfile.sandbox. The home is
// chowned to it: if the control plane (running as root in its container)
// creates the directory, the sandbox user could otherwise not write into it.
const (
	sandboxUID = 1001
	sandboxGID = 1001
)

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
		if err := p.ensureNetworkIsolation(ctx); err != nil {
			return nil, fmt.Errorf("prepare egress network: %w", err)
		}
		proxyURL := egressURLWithCreds("http://"+egressProxyAlias+":8888", spec.AgentID.String(), spec.EgressToken)
		args = append(args, "--network", egressNetwork)
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

	var dind *dindSidecar
	if spec.EnableDocker {
		d, err := p.startDind(ctx, spec)
		if err != nil {
			return nil, fmt.Errorf("start docker-in-docker sidecar: %w", err)
		}
		dind = d
		args = append(args, "-e", "DOCKER_HOST=tcp://"+dindAlias+":2375")
	}

	for k, v := range spec.Env {
		if k == "COVEY_WS_URL" {
			v = rewriteLoopbackForDocker(v)
		}
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, p.Image)

	out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput()
	if err != nil {
		if dind != nil {
			dind.stop(ctx, p.docker())
		}
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "pull access denied") ||
			strings.Contains(msg, "Unable to find image") {
			return nil, fmt.Errorf("sandbox image %q is missing — build it with `make sandbox-image`: %s", p.Image, msg)
		}
		return nil, fmt.Errorf("docker run: %v: %s", err, msg)
	}
	if dind != nil {
		// Only now, with the sandbox container created, can it be attached to
		// the sidecar's network — "docker run --network" takes only one
		// network, and the egress network (if hard isolation is active) already
		// claimed that slot above.
		connOut, err := exec.CommandContext(ctx, p.docker(), "network", "connect", dind.network, name).CombinedOutput()
		if err != nil {
			dind.stop(ctx, p.docker())
			_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()
			return nil, fmt.Errorf("attach sandbox to its docker-in-docker network: %v: %s", err, strings.TrimSpace(string(connOut)))
		}
	}
	return &dockerSandbox{docker: p.docker(), name: name, dind: dind}, nil
}

type dockerSandbox struct {
	docker string
	name   string
	// dind is the agent's Docker-in-Docker sidecar, nil if it never got one.
	dind *dindSidecar
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
	if s.dind != nil {
		s.dind.stop(stopCtx, s.docker)
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
	if out, err := exec.CommandContext(ctx, p.docker(), "image", "inspect", p.Image).CombinedOutput(); err != nil {
		problems = append(problems, fmt.Sprintf(
			"sandbox image %q is missing — build it once: "+
				"`docker build -f Dockerfile.sandbox -t %s .` (%s)",
			p.Image, p.Image, firstLine(string(out), err)))
	}
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

// ensureNetworkIsolation idempotently establishes the internal network and the
// running proxy container — the two building blocks of the hard egress mode.
func (p *DockerProvider) ensureNetworkIsolation(ctx context.Context) error {
	if err := p.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	return p.ensureEgressProxy(ctx)
}

// ensureEgressNetwork creates the internal network (no gateway to the outside).
func (p *DockerProvider) ensureEgressNetwork(ctx context.Context) error {
	if err := exec.CommandContext(ctx, p.docker(), "network", "inspect", egressNetwork).Run(); err == nil {
		return nil // already exists
	}
	out, err := exec.CommandContext(ctx, p.docker(), "network", "create", "--internal", egressNetwork).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("create internal network: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureEgressProxy starts the proxy container if it is not running: on the
// internal network (alias covey-egress) as the only way out, plus the default
// bridge network for internet access.
func (p *DockerProvider) ensureEgressProxy(ctx context.Context) error {
	// Already running?
	out, _ := exec.CommandContext(ctx, p.docker(), "inspect", "-f", "{{.State.Running}}", egressProxyName).Output()
	if strings.TrimSpace(string(out)) == "true" {
		return nil
	}
	// Clear leftovers so the name is free.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", egressProxyName).Run()

	image := p.EgressProxyImage
	if image == "" {
		image = "covey-egress:latest"
	}
	args := []string{"run", "-d", "--restart", "unless-stopped",
		"--name", egressProxyName,
		"--network", egressNetwork,
		"--network-alias", egressProxyAlias,
		"--add-host", "host.docker.internal:host-gateway",
	}
	for k, v := range p.EgressProxyEnv {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, image)
	if runOut, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(runOut))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "Unable to find image") {
			return fmt.Errorf("egress proxy image %q is missing — build it with `make egress-image`: %s", image, msg)
		}
		return fmt.Errorf("start egress proxy: %v: %s", err, msg)
	}
	// Internet side: attach the default bridge network so the proxy reaches the
	// outside (and the DB/control plane via host-gateway).
	if connOut, err := exec.CommandContext(ctx, p.docker(), "network", "connect", "bridge", egressProxyName).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(connOut))
		if !strings.Contains(msg, "already exists") && !strings.Contains(msg, "already connected") {
			return fmt.Errorf("attach egress proxy to the bridge network: %v: %s", err, msg)
		}
	}
	return nil
}

// dindSidecar is one agent's own nested Docker daemon: real container
// isolation for whatever the agent starts inside it (e.g. `docker compose
// up`), fully separate from the host's own Docker socket and from every other
// agent's sidecar.
type dindSidecar struct {
	container string
	network   string // per-agent — torn down with the sidecar, never shared
}

func dindContainerName(agentID string) string { return "covey-dind-" + agentID }
func dindNetworkName(agentID string) string   { return "covey-dind-net-" + agentID }

// startDind brings up this agent's Docker-in-Docker sidecar, fresh on every
// wake (ephemeral compute, like the sandbox itself — spec/01). --privileged is
// required for a nested Docker daemon and is confined to this one sidecar
// container; the sandbox that talks to it over DOCKER_HOST stays exactly as
// unprivileged as any sandbox without Docker. This is the same pattern
// GitLab's own CI already relies on for Docker-in-Docker jobs, not a new
// risk invented here.
//
// The sidecar's own egress (image pulls) is routed through the SAME
// allowlist proxy as the sandbox's other traffic, by the same mechanism
// (cooperative HTTP(S)_PROXY, or attached to the hard-isolation network) —
// Docker-in-Docker must not become a side door around the org's egress
// policy, only a place to run containers.
func (p *DockerProvider) startDind(ctx context.Context, spec SandboxSpec) (*dindSidecar, error) {
	container := dindContainerName(spec.AgentID.String())
	network := dindNetworkName(spec.AgentID.String())

	// Clear leftovers of a crashed predecessor — both names have to be free.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", container).Run()
	_ = exec.CommandContext(ctx, p.docker(), "network", "rm", network).Run()

	if out, err := exec.CommandContext(ctx, p.docker(), "network", "create", network).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create docker-in-docker network: %v: %s", err, strings.TrimSpace(string(out)))
	}

	args := []string{"run", "-d", "--rm", "--privileged",
		"--name", container,
		"--network", network,
		"--network-alias", dindAlias,
		// Plaintext daemon: the private per-agent network is the boundary
		// here, not TLS — nothing outside this sandbox+sidecar pair can reach
		// this network at all.
		"-e", "DOCKER_TLS_CERTDIR=",
	}
	switch p.EgressIsolation {
	case "network":
		if err := p.ensureNetworkIsolation(ctx); err != nil {
			_ = exec.CommandContext(ctx, p.docker(), "network", "rm", network).Run()
			return nil, fmt.Errorf("prepare egress network: %w", err)
		}
		proxyURL := egressURLWithCreds("http://"+egressProxyAlias+":8888", spec.AgentID.String(), spec.EgressToken)
		for _, e := range proxyEnvVars(proxyURL, "localhost,127.0.0.1,::1") {
			args = append(args, "-e", e)
		}
	default:
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
		if p.EgressProxyURL != "" {
			proxyURL := egressURLWithCreds(p.EgressProxyURL, spec.AgentID.String(), spec.EgressToken)
			for _, e := range proxyEnvVars(proxyURL, "host.docker.internal,localhost,127.0.0.1,::1") {
				args = append(args, "-e", e)
			}
		}
	}
	args = append(args, DindImage)

	out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput()
	if err != nil {
		_ = exec.CommandContext(ctx, p.docker(), "network", "rm", network).Run()
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "Unable to find image") {
			return nil, fmt.Errorf("docker-in-docker image %q is missing — pull it once: `docker pull %s`: %s", DindImage, DindImage, msg)
		}
		return nil, fmt.Errorf("start docker-in-docker sidecar: %v: %s", err, msg)
	}

	if p.EgressIsolation == "network" {
		// Second network, for the same reason the sandbox itself cannot take
		// both at `run` time: the egress network only carries the proxy route,
		// the per-agent network is what makes the sidecar reachable as "dind".
		if connOut, err := exec.CommandContext(ctx, p.docker(), "network", "connect", egressNetwork, container).CombinedOutput(); err != nil {
			_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", container).Run()
			_ = exec.CommandContext(ctx, p.docker(), "network", "rm", network).Run()
			return nil, fmt.Errorf("attach docker-in-docker sidecar to the egress network: %v: %s", err, strings.TrimSpace(string(connOut)))
		}
	}

	return &dindSidecar{container: container, network: network}, nil
}

// stop tears the sidecar and its own per-agent network down — best-effort,
// mirroring dockerSandbox.Stop: a wake that never reaches Stop (crash, force
// kill) leaves both names free again on the NEXT wake's leftover-clearing in
// startDind, so nothing here needs to be perfect.
func (d *dindSidecar) stop(ctx context.Context, docker string) {
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	_ = exec.CommandContext(stopCtx, docker, "rm", "-f", d.container).Run()
	_ = exec.CommandContext(stopCtx, docker, "network", "rm", d.network).Run()
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
