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
)

// DockerProvider startet coveyd in einem Container — echte Isolation auf
// Namespace-Ebene statt Prozess-Ebene (spec/01: die Sandbox ist dumm und
// ersetzbar; nur das Home ist persistent und wird als Volume gemountet).
// Er spricht bewusst die docker-CLI statt der SDK-Libs: keine schwere
// Abhängigkeit, und `docker run`/`docker stop` sind die ganze Wahrheit.
// Der Container erbt nichts vom Host — die Umgebung besteht ausschließlich
// aus dem, was die Control Plane in SandboxSpec.Env hineingibt.
type DockerProvider struct {
	// Image ist das Sandbox-Image (coveyd + Runtime), siehe Dockerfile.sandbox.
	Image string
	// DataDir hält die persistenten Agenten-Homes auf dem Host.
	DataDir string
	// DockerBin überschreibt den CLI-Pfad (Default "docker") — für Tests.
	DockerBin string
	// EgressProxyURL leitet den ausgehenden HTTP(S)-Verkehr des Containers über
	// den Covey-Egress-Allowlist-Proxy (leer = kein Proxy). Der Wert ist die
	// container-seitige URL (i. d. R. http://host.docker.internal:<port>).
	// Kooperativ ("proxy"-Modus): die Control-Plane-Verbindung
	// (host.docker.internal) und Loopback bleiben via NO_PROXY am Proxy vorbei.
	EgressProxyURL string
	// EgressIsolation: "proxy" (kooperativ, Default) oder "network" (hart).
	// Im network-Modus läuft die Sandbox auf einem internen Docker-Netz ohne
	// Internet; ein Proxy-Container ist der einzige Ausgang und erzwingt die
	// Allowlist — nicht mehr vom Agenten umgehbar. Die Control-Plane-WS läuft
	// dann per HTTP-CONNECT durch den Proxy (erfordert TLS/wss, siehe Doku).
	EgressIsolation string
	// EgressProxyImage ist das Proxy-Image für den network-Modus (make egress-image).
	EgressProxyImage string
	// EgressProxyEnv sind die ENV-Variablen des Proxy-Containers (DB-URL,
	// COVEY_EGRESS_ALLOW inkl. Control-Plane-Host, COVEY_EGRESS_PROXY_ADDR).
	EgressProxyEnv map[string]string
}

// Namen der Bausteine des harten Isolationsmodus.
const (
	egressNetwork    = "covey-egress-internal" // internes Netz ohne Internet
	egressProxyName  = "covey-egress-proxy"    // der Proxy-Container
	egressProxyAlias = "covey-egress"          // DNS-Alias im internen Netz
)

// sandboxHome ist der feste Home-Pfad im Container (User `agent` im Image).
const sandboxHome = "/home/agent"

// sandboxUID/GID ist der User `agent` aus Dockerfile.sandbox. Das Home wird
// darauf gechownt: legt die Control Plane (als root im Container) das
// Verzeichnis an, könnte der Sandbox-User sonst nicht hineinschreiben.
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

func (p *DockerProvider) Start(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	home := spec.HomeDir
	if home == "" {
		home = filepath.Join(p.DataDir, "homes", spec.AgentID.String())
	}
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	// Best effort: gelingt nur als root (Deployment-Container); lokal läuft
	// der Prozess als normaler User und Docker Desktop mappt die Ownership.
	_ = os.Chown(home, sandboxUID, sandboxGID)

	name := containerName(spec.AgentID.String())
	// Reste einer abgestürzten Vorgänger-Sandbox wegräumen — der Name muss frei sein.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()

	// --init: die Runtime spawnt Kindprozesse; tini als PID 1 erbt und reapt
	// sie, coveyd bekommt SIGTERM trotzdem sauber durchgereicht.
	args := []string{"run", "-d", "--rm", "--init",
		"--name", name,
		"-v", home + ":" + sandboxHome,
		"-e", "HOME=" + sandboxHome,
		"-e", "COVEY_HOME=" + sandboxHome,
	}

	switch p.EgressIsolation {
	case "network":
		// Harte Isolation: Sandbox nur im internen Netz (kein Internet). Der
		// einzige Ausgang ist der Proxy-Container; auch die Control-Plane-WS
		// läuft per CONNECT durch ihn — deshalb hier KEIN host.docker.internal-
		// add-host, sondern der Proxy als HTTP(S)_PROXY. Loopback (Action-Proxy
		// des Daemons) bleibt direkt.
		if err := p.ensureNetworkIsolation(ctx); err != nil {
			return nil, fmt.Errorf("egress-netz vorbereiten: %w", err)
		}
		proxyURL := egressURLWithCreds("http://"+egressProxyAlias+":8888", spec.AgentID.String(), spec.EgressToken)
		args = append(args, "--network", egressNetwork)
		for _, e := range proxyEnvVars(proxyURL, "localhost,127.0.0.1,::1") {
			args = append(args, "-e", e)
		}
	default:
		// Kooperativ oder ohne Egress: --add-host macht die Control Plane als
		// host.docker.internal erreichbar (Docker Desktop bringt das mit).
		args = append(args, "--add-host", "host.docker.internal:host-gateway")
		if p.EgressProxyURL != "" {
			// Ausgehender Verkehr über den Allowlist-Proxy; Control Plane und
			// Loopback bleiben direkt (NO_PROXY), sonst würde die Daemon-WS
			// mitgeleitet. Die Proxy-URL trägt das per-Sandbox-Token, über das
			// der Proxy den Agenten identifiziert.
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
	args = append(args, p.Image)

	out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such image") || strings.Contains(msg, "pull access denied") ||
			strings.Contains(msg, "Unable to find image") {
			return nil, fmt.Errorf("sandbox-image %q fehlt — mit `make sandbox-image` bauen: %s", p.Image, msg)
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
	// docker stop = SIGTERM, nach Timeout SIGKILL; --rm räumt den Container weg.
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(stopCtx, s.docker, "stop", "-t", "5", s.name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		// Hart nachräumen, damit der Name für den nächsten Wake frei ist.
		_ = exec.CommandContext(stopCtx, s.docker, "rm", "-f", s.name).Run()
	}
	return nil
}

// containerName leitet einen stabilen, docker-tauglichen Namen aus der
// Agent-ID ab — pro Agent existiert höchstens eine Sandbox (seriell, spec/03).
func containerName(agentID string) string {
	return "covey-sandbox-" + agentID
}

// egressURLWithCreds hängt das per-Sandbox-Token als Proxy-Credentials an die
// Proxy-URL (agentID:token). Der Proxy identifiziert den Agenten daraus. Ohne
// Token bleibt die URL unverändert (der Proxy antwortet dann fail-closed 407).
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

// proxyEnvVars liefert die HTTP(S)_PROXY/NO_PROXY-Variablen (Groß- und
// Kleinschreibung, weil Tools mal die eine, mal die andere lesen).
func proxyEnvVars(proxyURL, noProxy string) []string {
	return []string{
		"HTTP_PROXY=" + proxyURL, "http_proxy=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL, "https_proxy=" + proxyURL,
		"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
	}
}

// ensureNetworkIsolation stellt idempotent das interne Netz und den laufenden
// Proxy-Container her — die beiden Bausteine des harten Egress-Modus.
func (p *DockerProvider) ensureNetworkIsolation(ctx context.Context) error {
	if err := p.ensureEgressNetwork(ctx); err != nil {
		return err
	}
	return p.ensureEgressProxy(ctx)
}

// ensureEgressNetwork legt das interne Netz an (kein Gateway nach außen).
func (p *DockerProvider) ensureEgressNetwork(ctx context.Context) error {
	if err := exec.CommandContext(ctx, p.docker(), "network", "inspect", egressNetwork).Run(); err == nil {
		return nil // existiert bereits
	}
	out, err := exec.CommandContext(ctx, p.docker(), "network", "create", "--internal", egressNetwork).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("internes netz anlegen: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ensureEgressProxy startet den Proxy-Container, falls er nicht läuft: am
// internen Netz (Alias covey-egress) als einziger Ausgang, plus das Default-
// Bridge-Netz für den Internet-Zugang.
func (p *DockerProvider) ensureEgressProxy(ctx context.Context) error {
	// Läuft er schon?
	out, _ := exec.CommandContext(ctx, p.docker(), "inspect", "-f", "{{.State.Running}}", egressProxyName).Output()
	if strings.TrimSpace(string(out)) == "true" {
		return nil
	}
	// Reste wegräumen, damit der Name frei ist.
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
			return fmt.Errorf("egress-proxy-image %q fehlt — mit `make egress-image` bauen: %s", image, msg)
		}
		return fmt.Errorf("egress-proxy starten: %v: %s", err, msg)
	}
	// Internet-Seite: das Default-Bridge-Netz dranhängen, damit der Proxy nach
	// außen (und via host-gateway zur DB/Control Plane) kommt.
	if connOut, err := exec.CommandContext(ctx, p.docker(), "network", "connect", "bridge", egressProxyName).CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(connOut))
		if !strings.Contains(msg, "already exists") && !strings.Contains(msg, "already connected") {
			return fmt.Errorf("egress-proxy ans bridge-netz hängen: %v: %s", err, msg)
		}
	}
	return nil
}

// rewriteLoopbackForDocker biegt eine Loopback-URL der Control Plane auf
// host.docker.internal um: „localhost" zeigt im Container auf den Container
// selbst, nicht auf den Host. Nicht-Loopback-URLs (echter Hostname/IP in
// COVEY_PUBLIC_URL) bleiben unangetastet.
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
