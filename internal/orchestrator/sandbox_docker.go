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
}

// sandboxHome ist der feste Home-Pfad im Container (User `agent` im Image).
const sandboxHome = "/home/agent"

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

	name := containerName(spec.AgentID.String())
	// Reste einer abgestürzten Vorgänger-Sandbox wegräumen — der Name muss frei sein.
	_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()

	// --init: die Runtime spawnt Kindprozesse; tini als PID 1 erbt und reapt
	// sie, coveyd bekommt SIGTERM trotzdem sauber durchgereicht.
	// --add-host: macht die Control Plane auf dem Host auch unter Linux als
	// host.docker.internal erreichbar (Docker Desktop bringt das von sich aus mit).
	args := []string{"run", "-d", "--rm", "--init",
		"--name", name,
		"--add-host", "host.docker.internal:host-gateway",
		"-v", home + ":" + sandboxHome,
		"-e", "HOME=" + sandboxHome,
		"-e", "COVEY_HOME=" + sandboxHome,
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
