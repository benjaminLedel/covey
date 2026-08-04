// Package dev turns the sandbox into the agent's own computer: run commands
// (exec) and manage long-running processes (start/stop/logs/list) — dev
// servers, databases, headless Chrome. Everything runs in the sandbox daemon,
// nothing on the control plane; the plugin needs no secrets
// (Descriptor.NoCredentials). ACCESS.md, activation and guard-rails apply as
// with any target system.
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
	// maxLogBytes caps the log buffer per process (the last output is what counts).
	maxLogBytes = 256 << 10
	// maxProcs caps the number of concurrently managed processes (runaway guard).
	maxProcs = 16
	// stopGrace is the grace period between SIGTERM and SIGKILL when stopping.
	stopGrace = 5 * time.Second
)

// logBuffer is a concurrently writable buffer that keeps only the last
// maxLogBytes — enough to see errors without blowing up the context.
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

// Tail returns the last n lines (n<=0: everything buffered).
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

// process is a managed background process along with its output buffer.
type process struct {
	name      string
	command   string
	cmd       *exec.Cmd
	buf       *logBuffer
	startedAt time.Time
	done      chan struct{}

	mu       sync.Mutex
	exitDesc string // empty as long as the process is running
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

// supervisor manages the background processes of a sandbox session. Every
// process gets its own process group (Setpgid) so that stop and shutdown also
// reach child processes (e.g. the app started by `sh -c`).
type supervisor struct {
	mu    sync.Mutex
	procs map[string]*process
}

var super = &supervisor{procs: map[string]*process{}}

func (s *supervisor) start(name, command, dir string) (map[string]any, error) {
	name = strings.TrimSpace(name)
	if name == "" || strings.TrimSpace(command) == "" {
		return nil, fmt.Errorf("name or cmd missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.procs[name]; ok && p.running() {
		return nil, fmt.Errorf("process %q is already running (pid %d) — stop it first", name, p.cmd.Process.Pid)
	}
	running := 0
	for _, p := range s.procs {
		if p.running() {
			running++
		}
	}
	if running >= maxProcs {
		return nil, fmt.Errorf("limit of %d running processes reached — clean up with stop", maxProcs)
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
		return nil, fmt.Errorf("no process %q — list shows them all", name)
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

// shutdown kills all running processes hard — the cleanup hook when the daemon
// shuts down (target.Shutdown). Without it, Chrome and dev servers would
// outlive the sandbox.
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

// killGroup signals the whole process group (negative PID); errors do not
// matter — the group may already be gone.
func killGroup(p *process, sig syscall.Signal) {
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}
