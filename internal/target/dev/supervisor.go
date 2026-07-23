// Package dev macht die Sandbox zum eigenen Computer des Agenten: Befehle
// ausführen (exec) und langlaufende Prozesse verwalten (start/stop/logs/list)
// — Dev-Server, Datenbanken, headless Chrome. Alles läuft im Daemon der
// Sandbox, nichts auf der Control Plane; Secrets braucht das Plugin keine
// (Descriptor.NoCredentials). ACCESS.md, Aktivierung und Guard-Rails greifen
// wie bei jedem Zielsystem.
package dev

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// maxLogBytes begrenzt den Log-Puffer je Prozess (die letzte Ausgabe zählt).
	maxLogBytes = 256 << 10
	// maxProcs begrenzt die gleichzeitig verwalteten Prozesse (Runaway-Guard).
	maxProcs = 16
	// stopGrace ist die Frist zwischen SIGTERM und SIGKILL beim Stoppen.
	stopGrace = 5 * time.Second
)

// logBuffer ist ein nebenläufig beschreibbarer Puffer, der nur die letzten
// maxLogBytes behält — genug, um Fehler zu sehen, ohne den Kontext zu sprengen.
type logBuffer struct {
	mu        sync.Mutex
	buf       []byte
	truncated bool
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxLogBytes {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-maxLogBytes:]...)
		b.truncated = true
	}
	return len(p), nil
}

// Tail liefert die letzten n Zeilen (n<=0: alles Gepufferte).
func (b *logBuffer) Tail(n int) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := strings.TrimRight(string(b.buf), "\n")
	if s == "" {
		return "", b.truncated
	}
	lines := strings.Split(s, "\n")
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), b.truncated
}

// process ist ein verwalteter Hintergrund-Prozess samt Ausgabe-Puffer.
type process struct {
	name      string
	command   string
	cmd       *exec.Cmd
	buf       *logBuffer
	startedAt time.Time
	done      chan struct{}

	mu       sync.Mutex
	exitDesc string // leer, solange der Prozess läuft
}

func (p *process) running() bool {
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}

func (p *process) exitInfo() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exitDesc
}

func (p *process) info() map[string]any {
	out := map[string]any{
		"name":    p.name,
		"cmd":     p.command,
		"pid":     p.cmd.Process.Pid,
		"running": p.running(),
	}
	if p.running() {
		out["uptime_secs"] = int(time.Since(p.startedAt).Seconds())
	} else {
		out["exit"] = p.exitInfo()
	}
	return out
}

// supervisor verwaltet die Hintergrund-Prozesse einer Sandbox-Session.
// Jeder Prozess bekommt eine eigene Prozessgruppe (Setpgid), damit stop und
// shutdown auch Kind-Prozesse (z. B. die von `sh -c` gestartete App) treffen.
type supervisor struct {
	mu    sync.Mutex
	procs map[string]*process
}

var super = &supervisor{procs: map[string]*process{}}

func (s *supervisor) start(name, command, dir string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("name oder cmd fehlt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[name]; ok && p.running() {
		return nil, fmt.Errorf("prozess %q läuft bereits (pid %d) — erst stop", name, p.cmd.Process.Pid)
	}
	running := 0
	for _, p := range s.procs {
		if p.running() {
			running++
		}
	}
	if running >= maxProcs {
		return nil, fmt.Errorf("limit von %d laufenden prozessen erreicht — räume mit stop auf", maxProcs)
	}

	cmd := exec.Command("sh", "-c", command)
	cmd.Dir = dir
	buf := &logBuffer{}
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", name, err)
	}
	p := &process{name: name, command: command, cmd: cmd, buf: buf,
		startedAt: time.Now(), done: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if err != nil {
			p.exitDesc = err.Error()
		} else {
			p.exitDesc = "exit 0"
		}
		p.mu.Unlock()
		close(p.done)
	}()
	s.procs[name] = p
	return map[string]any{"name": name, "pid": cmd.Process.Pid, "status": "running"}, nil
}

func (s *supervisor) get(name string) (*process, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.procs[strings.TrimSpace(name)]
	if !ok {
		return nil, fmt.Errorf("kein prozess %q — list zeigt alle", name)
	}
	return p, nil
}

func (s *supervisor) stop(name string) (map[string]any, error) {
	p, err := s.get(name)
	if err != nil {
		return nil, err
	}
	killGroup(p, syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(stopGrace):
		killGroup(p, syscall.SIGKILL)
		<-p.done
	}
	return map[string]any{"name": p.name, "status": "stopped", "exit": p.exitInfo()}, nil
}

func (s *supervisor) logs(name string, tailLines int) (map[string]any, error) {
	p, err := s.get(name)
	if err != nil {
		return nil, err
	}
	if tailLines <= 0 {
		tailLines = 100
	}
	logs, truncated := p.buf.Tail(tailLines)
	out := map[string]any{"name": p.name, "running": p.running(), "logs": logs, "truncated": truncated}
	if !p.running() {
		out["exit"] = p.exitInfo()
	}
	return out, nil
}

func (s *supervisor) list() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	names := make([]string, 0, len(s.procs))
	for name := range s.procs {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, s.procs[name].info())
	}
	return out
}

// shutdown beendet alle laufenden Prozesse hart — der Aufräum-Hook beim
// Herunterfahren des Daemons (target.Shutdown). Ohne ihn überlebten Chrome
// und Dev-Server die Sandbox.
func (s *supervisor) shutdown() {
	s.mu.Lock()
	procs := make([]*process, 0, len(s.procs))
	for _, p := range s.procs {
		procs = append(procs, p)
	}
	s.mu.Unlock()
	for _, p := range procs {
		if p.running() {
			killGroup(p, syscall.SIGKILL)
			<-p.done
		}
	}
}

// killGroup signalisiert die ganze Prozessgruppe (negative PID); Fehler sind
// egal — die Gruppe kann bereits beendet sein.
func killGroup(p *process, sig syscall.Signal) {
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}
