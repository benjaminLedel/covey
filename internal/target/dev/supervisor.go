// Package dev turns the sandbox into the agent's own computer: run commands
// (exec) and manage long-running processes (start/stop/logs/list) — dev
// servers, databases, headless Chrome. Everything runs in the sandbox daemon,
// nothing on the control plane; the plugin needs no secrets
// (Descriptor.NoCredentials). ACCESS.md, activation and guard-rails apply as
// with any target system.
package dev

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	// jobsSubdir is where a job's record lives, relative to the agent home. The
	// home is the only persistent directory of the sandbox — that is exactly why
	// the record goes there and not into /tmp.
	jobsSubdir = ".covey/jobs"
	// maxJobLogBytes caps a job's log FILE. Larger than the buffer in memory,
	// because this one is meant to survive a run: a test suite's summary sits at
	// the end, and that is what the next run reads.
	maxJobLogBytes = 4 << 20
)

// logBuffer is a concurrently writable buffer that keeps only the last
// maxLogBytes — enough to see errors without blowing up the context. Whatever is
// written also goes into the job's log file, if there is one: that is what makes
// the output of a background process readable in the NEXT run.
type logBuffer struct {
	mu        sync.Mutex
	buf       []byte
	truncated bool
	file      *os.File
	written   int64
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > maxLogBytes {
		b.buf = append([]byte(nil), b.buf[len(b.buf)-maxLogBytes:]...)
		b.truncated = true
	}
	// The file is a convenience, never a reason to break the process: a full
	// disk must not kill the test suite whose output is being written here.
	if b.file != nil && b.written < maxJobLogBytes {
		if n, err := b.file.Write(p); err == nil {
			b.written += int64(n)
		}
	}
	return len(p), nil
}

func (b *logBuffer) closeFile() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		b.file.Close()
		b.file = nil
	}
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

// jobRecord is what a background process leaves behind in the persistent home:
// what ran where, since when, and how it ended. It exists because of one
// concrete failure: an agent starts a test suite as a job, its run hits the turn
// limit before the suite is finished — and the next run finds nothing but its
// own note that "the job was still running". The result was there; nobody could
// read it any more. With the record, both the exit code and the log outlive the
// run and even a cold sandbox.
type jobRecord struct {
	Name      string `json:"name"`
	Cmd       string `json:"cmd"`
	Cwd       string `json:"cwd"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	Exit      string `json:"exit,omitempty"`
}

// jobFileName maps a job name onto a file name — path separators and dots would
// otherwise let a name write outside the jobs directory.
func jobFileName(name string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	return strings.Trim(safe, "-")
}

// jobsDir is the record directory in the agent home; an empty home (tests,
// actions outside a sandbox) switches the persistence off silently.
func jobsDir(home string) string {
	if strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, jobsSubdir)
}

func writeJobRecord(home string, rec jobRecord) {
	dir := jobsDir(home)
	if dir == "" || jobFileName(rec.Name) == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, jobFileName(rec.Name)+".json"), data, 0o644)
}

func readJobRecord(home, name string) (jobRecord, bool) {
	dir := jobsDir(home)
	if dir == "" || jobFileName(name) == "" {
		return jobRecord{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, jobFileName(name)+".json"))
	if err != nil {
		return jobRecord{}, false
	}
	var rec jobRecord
	if json.Unmarshal(data, &rec) != nil {
		return jobRecord{}, false
	}
	return rec, true
}

// readJobLog returns the last tailLines lines of a finished job's log file.
func readJobLog(home, name string, tailLines int) (string, bool) {
	dir := jobsDir(home)
	if dir == "" || jobFileName(name) == "" {
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(dir, jobFileName(name)+".log"))
	if err != nil {
		return "", false
	}
	s := strings.TrimRight(string(data), "\n")
	if s == "" {
		return "", true
	}
	lines := strings.Split(s, "\n")
	if tailLines > 0 && len(lines) > tailLines {
		lines = lines[len(lines)-tailLines:]
	}
	return strings.Join(lines, "\n"), true
}

// listJobRecords reads all records of the home — the jobs of EARLIER runs, which
// the supervisor no longer knows in memory.
func listJobRecords(home string) map[string]jobRecord {
	out := map[string]jobRecord{}
	dir := jobsDir(home)
	if dir == "" {
		return out
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if rec, ok := readJobRecord(home, strings.TrimSuffix(e.Name(), ".json")); ok && rec.Name != "" {
			out[rec.Name] = rec
		}
	}
	return out
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

func (s *supervisor) start(name, command, dir, home string) (map[string]any, error) {
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
	// The log file next to the buffer: it is what the next run reads. A job
	// starting anew overwrites its predecessor's log — the record belongs to the
	// name, and the agent gave the name.
	if jd := jobsDir(home); jd != "" && jobFileName(name) != "" {
		if os.MkdirAll(jd, 0o755) == nil {
			if f, err := os.Create(filepath.Join(jd, jobFileName(name)+".log")); err == nil {
				buf.file = f
			}
		}
	}
	cmd.Stdout, cmd.Stderr = buf, buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		buf.closeFile()
		return nil, fmt.Errorf("start %q: %w", name, err)
	}
	p := &process{name: name, command: command, cmd: cmd, buf: buf,
		startedAt: time.Now(), done: make(chan struct{})}
	rec := jobRecord{Name: name, Cmd: command, Cwd: dir, PID: cmd.Process.Pid,
		StartedAt: time.Now().UTC().Format(time.RFC3339)}
	writeJobRecord(home, rec)
	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		if err != nil {
			p.exitDesc = err.Error()
		} else {
			p.exitDesc = "exit 0"
		}
		desc := p.exitDesc
		p.mu.Unlock()
		buf.closeFile()
		rec.EndedAt = time.Now().UTC().Format(time.RFC3339)
		rec.Exit = desc
		writeJobRecord(home, rec)
		close(p.done)
	}()
	s.procs[name] = p
	return map[string]any{"name": name, "pid": cmd.Process.Pid, "status": "running",
		"note": "output and exit code are recorded in the home — readable with logs even in a later run"}, nil
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

func (s *supervisor) logs(name string, tailLines int, home string) (map[string]any, error) {
	if tailLines <= 0 {
		tailLines = 100
	}
	p, err := s.get(name)
	if err != nil {
		// Not in memory — but perhaps a job from an earlier run. This is the
		// path that matters after a turn-limit abort: the suite ran, the run is
		// over, the result is on disk.
		rec, ok := readJobRecord(home, name)
		if !ok {
			return nil, err
		}
		logs, _ := readJobLog(home, name, tailLines)
		out := map[string]any{"name": rec.Name, "running": false, "logs": logs,
			"cmd": rec.Cmd, "cwd": rec.Cwd, "started_at": rec.StartedAt, "from_earlier_run": true}
		if rec.EndedAt != "" {
			out["exit"], out["ended_at"] = rec.Exit, rec.EndedAt
		} else {
			// Started, never finished: the sandbox went away underneath it. The
			// log up to that point is real, the missing outcome is not a green one.
			out["exit"] = "unknown — the process did not outlive its sandbox; the log ends where the run ended"
		}
		return out, nil
	}
	logs, truncated := p.buf.Tail(tailLines)
	out := map[string]any{"name": p.name, "running": p.running(), "logs": logs, "truncated": truncated}
	if !p.running() {
		out["exit"] = p.exitInfo()
	}
	return out, nil
}

func (s *supervisor) list(home string) []map[string]any {
	s.mu.Lock()
	live := make(map[string]*process, len(s.procs))
	for name, p := range s.procs {
		live[name] = p
	}
	s.mu.Unlock()

	// Jobs of earlier runs belong in the list too — otherwise an agent whose
	// sandbox was restarted sees an empty list and starts the suite it already
	// ran all over again.
	infos := map[string]map[string]any{}
	for name, rec := range listJobRecords(home) {
		info := map[string]any{"name": name, "cmd": rec.Cmd, "running": false,
			"started_at": rec.StartedAt, "from_earlier_run": true}
		if rec.EndedAt != "" {
			info["exit"], info["ended_at"] = rec.Exit, rec.EndedAt
		} else {
			info["exit"] = "unknown — did not outlive its sandbox"
		}
		infos[name] = info
	}
	for name, p := range live {
		infos[name] = p.info()
	}
	names := make([]string, 0, len(infos))
	for name := range infos {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		out = append(out, infos[name])
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
