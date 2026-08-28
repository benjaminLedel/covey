package runner

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/sandbox"
)

// Docker starts coveyd in a container — real isolation at the namespace level
// instead of the process level (spec/01: the sandbox is dumb and replaceable;
// only the home is persistent and gets mounted as a volume). It deliberately
// talks to the docker CLI instead of the SDK libs: no heavy dependency, and
// `docker run`/`docker stop` are the whole truth. The container inherits
// nothing from the host — the environment consists exclusively of what the
// control plane puts into StartSandbox.Env.
//
// It is the runner's arm, not the orchestrator's: which image an agent belongs
// in is decided on the control plane, which knows the agent. Here only images
// exist.
type Docker struct {
	// RunnerID is the runner this executor belongs to. It names the egress
	// segment and the proxy container: one set per runner, because a runner
	// serves exactly one organisation and `--internal` cuts the way out, not
	// the way sideways.
	RunnerID uuid.UUID
	// EgressRunnerToken is what the proxy container authenticates to the
	// control plane with.
	EgressRunnerToken string
	// Image is the fallback for a start that names none, and the image the
	// self-check asks about when the control plane names none. What an agent's
	// profile resolves to is decided over there — a runner knows images, not
	// agents.
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
	// EgressProxyEnv are the proxy container's env variables (COVEY_CONTROL_URL,
	// COVEY_EGRESS_ALLOW incl. the control plane host,
	// COVEY_EGRESS_PROXY_ADDR) — deliberately NOT the database URL: the proxy
	// is an enforcement point, not a database client (spec/16, "Trust
	// boundary"). The runner token is added from EgressRunnerToken.
	EgressProxyEnv map[string]string

	// proxyFresh: whether this process has already renewed its proxy
	// container. See ensureEgressProxy for why that is necessary.
	proxyMu    sync.Mutex
	proxyFresh bool
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

func (p *Docker) docker() string {
	if p.DockerBin != "" {
		return p.DockerBin
	}
	return "docker"
}

// AgentHome is the route to an agent's home: a directory on this host,
// mounted into the sandbox — readable and writable even while no container is
// running, and asleep is the normal state.
//
// uid/gid belong with it: the directory is chowned to the sandbox user, and
// whoever reads or writes it from outside has to know whose it is.
func (p *Docker) AgentHome(agentID uuid.UUID) (path string, uid, gid int) {
	return p.homePath(agentID.String()), sandboxUID, sandboxGID
}

// homePath is the single place where a home's host path is formed.
func (p *Docker) homePath(agentID string) string {
	home := filepath.Join(p.DataDir, "homes", agentID)
	if abs, err := filepath.Abs(home); err == nil {
		return abs
	}
	return home
}

// Start brings up the sandbox and returns the container's name.
// The second return value is what came up beside the sandbox — empty for an
// agent that declared no services.
func (p *Docker) Start(ctx context.Context, spec StartSandbox) (string, []sandbox.ServiceRun, error) {
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
		return "", nil, err
	}
	_ = os.Chown(home, sandboxUID, sandboxGID)

	name := containerName(spec.AgentID.String())
	// Clear leftovers of a crashed predecessor sandbox — the name has to be free.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()
	// And its services with it. They are bound to the sandbox's life, so one
	// that outlived its sandbox is a leftover too — with a database still
	// holding the state of a run that ended, which is the more expensive half.
	p.removeServices(ctx, spec.AgentID)

	// The services first: a daemon that reports `ready` should be reporting a
	// workplace that is complete. A failure here ends the start — an agent that
	// believes it has a database and has not would report the wrong defect.
	services, err := p.startServices(ctx, spec)
	if err != nil {
		return "", nil, err
	}

	// --init: the runtime spawns child processes; tini as PID 1 inherits and
	// reaps them, while coveyd still gets SIGTERM passed through cleanly.
	//
	// Deliberately WITHOUT --rm. A sandbox that dies at its start takes its own
	// output with it the moment docker removes it, and "exit 1" without a word
	// is the sentence that sends an operator onto the host — where the
	// container is already gone. So it stays until Wait has read its last lines
	// (logTail); removal happens there and in Stop, and Start above clears a
	// leftover of the same name before it begins.
	args := []string{"run", "-d", "--init",
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
			p.removeServices(context.WithoutCancel(ctx), spec.AgentID)
			return "", nil, fmt.Errorf("prepare egress network: %w", err)
		}
		proxyURL := egressURLWithCreds("http://"+egressProxyAlias+":8888", spec.AgentID.String(), spec.EgressToken)
		args = append(args, "--network", egressNetworkFor(p.RunnerID))
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
	image := spec.Image
	if image == "" {
		image = p.Image
	}
	args = append(args, image)

	out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput()
	if err != nil {

		// The services came up for a sandbox that never did. Nothing will stop
		// them later — no container name is registered anywhere — so they go
		// here.
		p.removeServices(context.WithoutCancel(ctx), spec.AgentID)
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "pull access denied") ||
			strings.Contains(msg, "Unable to find image") {
			return "", nil, fmt.Errorf("sandbox image %q is missing — %s: %s",
				image, buildHint(map[string]string{image: spec.ImageHint}, image), msg)
		}
		return "", nil, fmt.Errorf("docker run: %v: %s", err, msg)
	}
	if err := p.joinServices(ctx, spec, name); err != nil {
		// A sandbox that cannot reach its services is not a sandbox with a
		// smaller workplace; it is one whose declaration is a lie. Both halves
		// go, and the start fails with the reason.
		_ = exec.CommandContext(context.WithoutCancel(ctx), p.docker(), "rm", "-f", name).Run()
		p.removeServices(context.WithoutCancel(ctx), spec.AgentID)
		return "", nil, err
	}
	return name, services, nil
}

// Stop shuts the compute instance down; the home stays.
func (p *Docker) Stop(ctx context.Context, name string) error {
	// docker stop = SIGTERM, SIGKILL after the timeout; --rm removes the container.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(stopCtx, p.docker(), "stop", "-t", "5", name).CombinedOutput()
	if err != nil && strings.Contains(string(out), "No such container") {
		return nil
	}
	// Always, not only after an error: without --rm the container stays behind
	// after a clean stop too, and the next wake needs the name.
	_ = exec.CommandContext(stopCtx, p.docker(), "rm", "-f", name).Run()
	// The services live exactly as long as the sandbox does. Whatever state
	// they hold is scratch by definition — what an agent keeps lives in its
	// home (spec/01), and a database that outlived its run would hand the next
	// one a state nobody wrote down.
	if agentID, ok := agentIDFromContainer(name); ok {
		p.removeServices(stopCtx, agentID)
	}
	return nil
}

// Wait blocks until the container has ended and describes how. This is what
// turns a crash into a reported fact: until now the control plane inferred it
// from a ReadyTimeout minutes later, or from a daemon link that went quiet.
func (p *Docker) Wait(ctx context.Context, name string) string {
	out, err := exec.CommandContext(ctx, p.docker(), "wait", name).CombinedOutput()
	if ctx.Err() != nil {
		return ""
	}
	code := strings.TrimSpace(string(out))
	if err != nil {
		// The container is already gone — somebody removed it, and the caller
		// decides whether anyone asked for that.
		return "container gone: " + firstLine(string(out), err)
	}
	// Read before removing: this is the only moment the container's own words
	// are still available, and they are what the whole report is worth.
	tail := p.logTail(ctx, name)
	defer func() {
		rmCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		_ = exec.CommandContext(rmCtx, p.docker(), "rm", "-f", name).Run()
		// This is the path nobody asked for — a crash, an OOM kill, an exit 0
		// the control plane did not order. Stop will not run for it, so the
		// services would stay behind here of all places: on a host that has
		// just shown it is short of something.
		if agentID, ok := agentIDFromContainer(name); ok {
			p.removeServices(rmCtx, agentID)
		}
	}()
	switch code {
	case "0":
		return "sandbox ended by itself (exit 0)" + tail
	case "137":
		// 128+9: killed. In a container that is nearly always the OOM killer,
		// and naming it saves a search that otherwise starts at the runtime.
		return "sandbox killed (exit 137 — out of memory?)" + tail
	default:
		return "sandbox ended by itself (exit " + code + ")" + tail
	}
}

// logTailLines/logTailBytes bound what travels. The report goes into the
// recording of a run, where it is read by a human looking for the reason — the
// last lines carry it, and a whole log would bury it as reliably as no log at
// all.
const (
	logTailLines = 40
	logTailBytes = 4000
)

// logTail is the container's last output, prepared for a one-line report.
// Empty when there is nothing to say — a container that dies silently should
// not produce a sentence pretending otherwise.
func (p *Docker) logTail(ctx context.Context, name string) string {
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(logCtx, p.docker(), "logs",
		"--tail", strconv.Itoa(logTailLines), name).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil && text == "" {
		return ""
	}
	if text == "" {
		return ""
	}
	if len(text) > logTailBytes {
		// From the END: the last lines are the ones that say why.
		text = "…" + text[len(text)-logTailBytes:]
	}
	return " — its last output: " + text
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
// HasImage: is this image already on the host? Asked before a start so that a
// download can be announced as one — `docker run` fetches it silently, and the
// difference between "downloading four gigabytes" and "hanging" is the whole
// question somebody has while they wait.
func (p *Docker) HasImage(ctx context.Context, image string) bool {
	if image == "" {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, p.docker(), "image", "inspect", image).Run() == nil
}

func (p *Docker) Check(ctx context.Context, req Check) ([]string, map[string]bool) {
	images, hints := req.Images, req.Hints
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, p.docker(), "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		// No daemon: then nothing can be said about any image either. An empty
		// presence map is the honest answer — "not there" would claim a finding
		// that was never made.
		return []string{fmt.Sprintf(
			"no Docker daemon reachable — every wake fails at starting the sandbox. "+
				"Running the control plane in a container? Then it needs the host's socket: "+
				"`-v /var/run/docker.sock:/var/run/docker.sock`. (%s)",
			firstLine(string(out), err))}, nil
	}

	var problems []string
	// Since the image hangs off the agent, "is the image there?" is no longer
	// one question but one per image in use. Asking about every configured
	// profile instead would warn every fresh installation about a dev image
	// nobody wants — the answer has to follow the agents, not the config.
	for _, image := range p.imagesWanted(images) {
		out, err := exec.CommandContext(ctx, p.docker(), "image", "inspect", image).CombinedOutput()
		if err == nil {
			continue
		}
		// Ein veröffentlichtes Image ist nicht abwesend, es liegt nur noch
		// nicht hier: `docker run` holt es beim ersten Wecken selbst. Das als
		// „Data Plane nicht bereit" zu melden, sagt einer frischen
		// Installation, sie sei kaputt — und riete ihr zu einem Bau, den sie
		// nicht braucht und in einem Container nicht ausführen kann. Dass es
		// noch nicht da ist, steht ohnehin an der Auswahl des Arbeitsplatzes
		// (Workplaces meldet es je Image).
		if sandbox.Pullable(image) {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"sandbox image %q is missing — %s (%s)",
			image, buildHint(hints, image), firstLine(string(out), err)))
	}
	if p.EgressIsolation == "network" && p.EgressProxyImage != "" {
		if out, err := exec.CommandContext(ctx, p.docker(), "image", "inspect", p.EgressProxyImage).CombinedOutput(); err != nil {
			problems = append(problems, fmt.Sprintf(
				"egress proxy image %q is missing, and hard isolation needs it — "+
					"build it once: `docker build -f Dockerfile.egress -t %s .` (%s)",
				p.EgressProxyImage, p.EgressProxyImage, firstLine(string(out), err)))
		}
	}

	// The interface's list of workplaces: asked for, not warned about. A fresh
	// installation has no dev image and needs none — but the dropdown may say
	// so before somebody picks it and finds out at the next wake.
	var present map[string]bool
	for _, image := range req.Report {
		if present == nil {
			present = map[string]bool{}
		}
		if _, seen := present[image]; seen {
			continue
		}
		err := exec.CommandContext(ctx, p.docker(), "image", "inspect", image).Run()
		present[image] = err == nil
	}
	return problems, present
}

// imagesWanted is what to ask about: the images the control plane named,
// which are the ones the agents actually work in. Without a list, the runner's
// own default is all it can say something about.
func (p *Docker) imagesWanted(images []string) []string {
	if len(images) == 0 {
		return []string{p.Image}
	}
	sort.Strings(images) // a stable order keeps the startup log comparable
	return images
}

// buildHint names how one obtains this image. A message that sends someone to
// `make sandbox-image` while the dev image is what is missing costs them the
// build twice — and the second one still does not help.
//
// The hint comes from the control plane, because only it can give it: the
// runner sees an image reference, and which profile it belongs to — and whether
// the instance has renamed it — is known over there. The catalogue is asked
// only as a fallback, for a message that reaches here without one; and it stays
// silent about a foreign image, because a `make` target that would build
// something else is worse than no advice.
func buildHint(hints map[string]string, image string) string {
	hint := strings.TrimSpace(hints[image])
	if hint == "" {
		hint = sandbox.BuildHint(nil, image)
	}
	if hint != "" {
		// Zwei Wege, weil es zwei Installationsarten gibt — und die zweite las
		// bis hierher eine Anweisung, die sie nicht ausfuehren kann: Wer Covey
		// als Container betreibt, hat kein Repository und damit kein `make`.
		// Fuer sie steht das fertige Image bereit, und die Variable weist es
		// dem Profil zu. Der Bau bleibt zuerst genannt, weil er auch ohne Netz
		// und ohne Vertrauen in eine fremde Registry auskommt.
		if env, ready := sandbox.EnvVarFor(nil, image), sandbox.PublicImageFor(nil, image); env != "" && ready != "" {
			return "build it once: `" + hint + "` — or take the published image: `" +
				env + "=" + ready + "`, then restart"
		}
		if env := sandbox.EnvVarFor(nil, image); env != "" {
			return "build it once: `" + hint + "`; without a checkout (container install) set `" +
				env + "` to an image you have and restart"
		}
		return "build it once: `" + hint + "`"
	}
	return "it is not one of the profiles from the catalogue — pull or build it yourself"
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
// running proxy container — the two building blocks of the hard egress mode,
// one set per runner.
func (p *Docker) ensureNetworkIsolation(ctx context.Context) error {
	if err := p.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	return p.ensureEgressProxy(ctx)
}

// ensureEgressNetwork creates the internal network (no gateway to the outside).
func (p *Docker) ensureEgressNetwork(ctx context.Context) error {
	name := egressNetworkFor(p.RunnerID)
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
func (p *Docker) ensureEgressProxy(ctx context.Context) error {
	name := egressProxyNameFor(p.RunnerID)

	p.proxyMu.Lock()
	fresh := p.proxyFresh
	p.proxyFresh = true
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
		"--network", egressNetworkFor(p.RunnerID),
		"--network-alias", egressProxyAlias,
		"--add-host", "host.docker.internal:host-gateway",
	}
	for k, v := range p.EgressProxyEnv {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, "-e", "COVEY_RUNNER_TOKEN="+p.EgressRunnerToken)
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

// Names of the egress objects as they were before they carried a runner: one
// internal network and one proxy container for the whole instance. Nothing
// uses them any more.
const (
	legacyEgressNetwork = "covey-egress-internal"
	legacyEgressProxy   = "covey-egress-proxy"
)

// PruneLegacyEgress clears away the instance-wide egress objects of earlier
// versions. Since the segment carries the runner's identity, the old ones are
// left behind at an upgrade — an orphaned container that keeps restarting and
// an internal network nobody joins.
//
// In the binary and not in the upgrade guide, on purpose: whoever installs
// Covey from GitHub gets the tooling with it, and an upgrade step that has to
// be read and typed is one that is skipped.
//
// Best effort throughout. A network that still has a sandbox from before
// attached cannot be removed, and that is not worth an error — the next start
// tries again, and until then the leftover costs nothing but a line in
// `docker network ls`.
func PruneLegacyEgress(ctx context.Context, dockerBin string, log *slog.Logger) {
	if dockerBin == "" {
		dockerBin = "docker"
	}
	out, err := exec.CommandContext(ctx, dockerBin, "inspect", "-f", "{{.Id}}", legacyEgressProxy).Output()
	if err != nil && len(out) == 0 {
		// Neither of them is there: a fresh installation, or already cleaned.
		if err := exec.CommandContext(ctx, dockerBin, "network", "inspect", legacyEgressNetwork).Run(); err != nil {
			return
		}
	}
	if err := exec.CommandContext(ctx, dockerBin, "rm", "-f", legacyEgressProxy).Run(); err == nil {
		log.Info("egress proxy of an earlier version removed — the segment now carries the runner",
			"container", legacyEgressProxy)
	}
	if err := exec.CommandContext(ctx, dockerBin, "network", "rm", legacyEgressNetwork).Run(); err == nil {
		log.Info("internal egress network of an earlier version removed", "network", legacyEgressNetwork)
	}
}

// Pull fetches an image onto this host. It is what `docker run` would do by
// itself at the first wake — done deliberately, so that the wait happens while
// somebody is looking instead of in front of the first agent of the day.
//
// No timeout of its own: several gigabytes over a slow line legitimately take
// their time, and the caller's context is the one that decides. The output is
// handed back on failure because the reason lives in it — no credentials for a
// private registry, a typo in the reference, no route to the host.
func (p *Docker) Pull(ctx context.Context, image string) (string, error) {
	out, err := exec.CommandContext(ctx, p.docker(), "pull", image).CombinedOutput()
	return string(out), err
}

// PullProgress is what a running pull can say about itself: how many bytes of
// the image are here, how many there are in total, and what docker is busy with
// at this moment.
type PullProgress struct {
	Bytes  int64
	Total  int64
	Detail string
}

// PullWatched fetches an image and says how far it has got while it does.
//
// It exists because the first start on a fresh host is the longest wait the
// platform has, and until now it was also the quietest: `docker run` fetches a
// missing image by itself, several gigabytes of it, and reports nothing until
// it is done. Whoever was watching saw an agent that had been "starting" for
// forty minutes and no way to tell a slow line from a hang.
//
// The figures come out of docker's own progress lines. Without a terminal it
// writes one line per update instead of redrawing, which is what makes them
// readable at all:
//
//	a1b2c3d4e5f6: Downloading [====>       ]  1.2GB/3.4GB
//	a1b2c3d4e5f6: Extracting  [==========> ]  3.4GB/3.4GB
//	a1b2c3d4e5f6: Pull complete
//
// Per layer the newest figure counts; the sum over the layers is the progress
// of the whole image. That sum is not exact — a layer docker has not started on
// yet is not in it, so the total grows during the first seconds — and it does
// not have to be. It answers "is this moving", and that is the question.
func (p *Docker) PullWatched(ctx context.Context, image string, watch func(PullProgress)) (string, error) {
	cmd := exec.CommandContext(ctx, p.docker(), "pull", image)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return "", err
	}

	var tail strings.Builder
	layers := map[string]PullProgress{}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if tail.Len() < logTailBytes {
			tail.WriteString(line)
			tail.WriteByte('\n')
		}
		id, pr, ok := parsePullLine(line)
		if !ok {
			continue
		}
		layers[id] = pr
		if watch == nil {
			continue
		}
		var sum PullProgress
		for _, l := range layers {
			sum.Bytes += l.Bytes
			sum.Total += l.Total
		}
		sum.Detail = pr.Detail
		watch(sum)
	}
	err = cmd.Wait()
	return tail.String(), err
}

// parsePullLine reads one of docker's progress lines. Anything else — "Using
// default tag", "Status: Downloaded newer image" — is not progress and is
// passed over rather than guessed at.
func parsePullLine(line string) (string, PullProgress, bool) {
	id, rest, ok := strings.Cut(line, ": ")
	if !ok || id == "" || strings.ContainsAny(id, " \t") {
		return "", PullProgress{}, false
	}
	rest = strings.TrimSpace(rest)
	status, figures, hasFigures := strings.Cut(rest, "[")
	status = strings.TrimSpace(status)
	if status == "" {
		return "", PullProgress{}, false
	}
	pr := PullProgress{Detail: status}
	if !hasFigures {
		// "Pull complete" / "Already exists": the layer is here, but how big it
		// was is not in this line. Whatever was counted for it last stands.
		return "", PullProgress{}, false
	}
	_, sizes, ok := strings.Cut(figures, "]")
	if !ok {
		return "", PullProgress{}, false
	}
	done, total, ok := strings.Cut(strings.TrimSpace(sizes), "/")
	if !ok {
		return "", PullProgress{}, false
	}
	pr.Bytes = parseDockerSize(done)
	pr.Total = parseDockerSize(total)
	if pr.Total == 0 {
		return "", PullProgress{}, false
	}
	return id, pr, true
}

// parseDockerSize reads docker's own way of writing a size ("1.234GB", "512B",
// "45.5kB"). Its units are decimal — that is docker's choice, and copying it
// here keeps our figure and the one in `docker pull` the same figure.
func parseDockerSize(s string) int64 {
	s = strings.TrimSpace(s)
	units := []struct {
		suffix string
		factor float64
	}{
		{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1},
	}
	for _, u := range units {
		if !strings.HasSuffix(s, u.suffix) {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), 64)
		if err != nil {
			return 0
		}
		return int64(v * u.factor)
	}
	return 0
}
