package dev

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"covey/internal/target"
)

func execute(t *testing.T, workdir, action, params string) any {
	t.Helper()
	ctx := target.WithWorkdir(context.Background(), workdir)
	res, err := System{}.Execute(ctx, action, json.RawMessage(params), target.Credential{})
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return res
}

func TestExec(t *testing.T) {
	dir := t.TempDir()
	res := execute(t, dir, "exec", `{"cmd":"echo hallo && pwd"}`).(map[string]any)
	if res["exit_code"] != 0 {
		t.Fatalf("exit_code: %+v", res)
	}
	out := res["output"].(string)
	if !strings.Contains(out, "hallo") || !strings.Contains(out, dir) {
		t.Fatalf("output must contain echo and workdir (cwd default): %q", out)
	}
}

func TestExecFailureIsResultNotError(t *testing.T) {
	res := execute(t, t.TempDir(), "exec", `{"cmd":"echo kaputt >&2; exit 3"}`).(map[string]any)
	if res["exit_code"] != 3 {
		t.Fatalf("exit code must come back as a result: %+v", res)
	}
	if !strings.Contains(res["output"].(string), "kaputt") {
		t.Fatalf("stderr must end up in the output: %+v", res)
	}
}

func TestExecTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	res := execute(t, t.TempDir(), "exec", `{"cmd":"sleep 30","timeout_secs":1}`).(map[string]any)
	if time.Since(start) > 5*time.Second {
		t.Fatal("the timeout did not take effect")
	}
	if res["exit_code"] != -1 || !strings.Contains(res["error"].(string), "timeout") {
		t.Fatalf("timeout must come back as an error result: %+v", res)
	}
}

func TestExecRelativeCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := execute(t, dir, "exec", `{"cmd":"pwd","cwd":"sub"}`).(map[string]any)
	if !strings.Contains(res["output"].(string), filepath.Join(dir, "sub")) {
		t.Fatalf("cwd must be resolved relative to the workdir: %+v", res)
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	dir := t.TempDir()
	res := execute(t, dir, "start",
		`{"name":"ticker","cmd":"while true; do echo tick; sleep 0.05; done"}`).(map[string]any)
	if res["status"] != "running" {
		t.Fatalf("start: %+v", res)
	}
	t.Cleanup(func() { super.shutdown() })

	// Starting the same name twice must be refused.
	ctx := target.WithWorkdir(context.Background(), dir)
	if _, err := (System{}).Execute(ctx, "start",
		json.RawMessage(`{"name":"ticker","cmd":"true"}`), target.Credential{}); err == nil {
		t.Fatal("a double start must fail")
	}

	time.Sleep(200 * time.Millisecond)
	logs := execute(t, dir, "logs", `{"name":"ticker"}`).(map[string]any)
	if logs["running"] != true || !strings.Contains(logs["logs"].(string), "tick") {
		t.Fatalf("logs: %+v", logs)
	}

	list := execute(t, dir, "list", `{}`).([]map[string]any)
	if len(list) != 1 || list[0]["name"] != "ticker" || list[0]["running"] != true {
		t.Fatalf("list: %+v", list)
	}

	pid := int(res["pid"].(int))
	stopped := execute(t, dir, "stop", `{"name":"ticker"}`).(map[string]any)
	if stopped["status"] != "stopped" {
		t.Fatalf("stop: %+v", stopped)
	}
	// The whole process group must be gone.
	if !groupFinished(pid) {
		t.Fatal("the process group is still alive after stop")
	}

	// After the stop the same name may start again.
	res = execute(t, dir, "start", `{"name":"ticker","cmd":"sleep 60"}`).(map[string]any)
	if res["status"] != "running" {
		t.Fatalf("restart: %+v", res)
	}
	execute(t, dir, "stop", `{"name":"ticker"}`)
}

func TestSupervisorShutdown(t *testing.T) {
	dir := t.TempDir()
	res := execute(t, dir, "start", `{"name":"langlaeufer","cmd":"sleep 60"}`).(map[string]any)
	pid := int(res["pid"].(int))
	super.shutdown()
	if !groupFinished(pid) {
		t.Fatal("shutdown must terminate all process groups")
	}
	list := execute(t, dir, "list", `{}`).([]map[string]any)
	for _, p := range list {
		if p["name"] == "langlaeufer" && p["running"] == true {
			t.Fatalf("process is still running after shutdown: %+v", p)
		}
	}
}

func TestParamValidation(t *testing.T) {
	ctx := target.WithWorkdir(context.Background(), t.TempDir())
	for name, call := range map[string][2]string{
		"exec without cmd":   {"exec", `{}`},
		"start without name": {"start", `{"cmd":"true"}`},
		"start without cmd":  {"start", `{"name":"x"}`},
		"stop unknown":       {"stop", `{"name":"gibtsnicht"}`},
		"logs unknown":       {"logs", `{"name":"gibtsnicht"}`},
		"unknown action":     {"quatsch", `{}`},
	} {
		if _, err := (System{}).Execute(ctx, call[0], json.RawMessage(call[1]), target.Credential{}); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
}

func TestActionSubjectAndDescriptor(t *testing.T) {
	if got := (System{}).ActionSubject("start", nil); got != "dev:start" {
		t.Fatalf("subject: %s", got)
	}
	d, ok := target.Describe("dev")
	if !ok || !d.NoCredentials {
		t.Fatal("dev must be registered and NoCredentials")
	}
	if _, err := (System{}).ParseWebhook(nil); err == nil {
		t.Fatal("dev has no webhook")
	}
}

func TestLogBufferCapsAndTails(t *testing.T) {
	b := &logBuffer{}
	for range 3 {
		b.Write(make([]byte, maxLogBytes))
	}
	if got, truncated := b.Tail(0); !truncated || len(got) > maxLogBytes {
		t.Fatalf("the buffer must cap: len=%d truncated=%v", len(got), truncated)
	}
	b = &logBuffer{}
	b.Write([]byte("a\nb\nc\n"))
	if got, _ := b.Tail(2); got != "b\nc" {
		t.Fatalf("tail: %q", got)
	}
}

// TestSubAgentAction covers handing the programming work to a sub-agent in the
// project checkout: the plugin itself drives no run, it passes the assignment
// through to the runner that the daemon puts into the context.
func TestSubAgentAction(t *testing.T) {
	var got target.SubAgentRequest
	ctx := target.WithSubAgent(target.WithWorkdir(context.Background(), t.TempDir()),
		func(_ context.Context, req target.SubAgentRequest) (target.SubAgentResult, error) {
			got = req
			return target.SubAgentResult{Result: "Fix erledigt", ChangedFiles: []string{"pkg/auth.go"}}, nil
		})

	res, err := System{}.Execute(ctx, "agent",
		json.RawMessage(`{"cwd":"repos/p1-main","task":"Behebe den Login-Bug","max_turns":40,"model":"claude-opus-5"}`),
		target.Credential{})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	if got.Dir != "repos/p1-main" || got.Task != "Behebe den Login-Bug" || got.MaxTurns != 40 || got.Model != "claude-opus-5" {
		t.Fatalf("assignment passed through wrongly: %+v", got)
	}
	out := res.(target.SubAgentResult)
	if out.Result != "Fix erledigt" || len(out.ChangedFiles) != 1 {
		t.Fatalf("wrong result: %+v", out)
	}
}

func TestSubAgentActionValidation(t *testing.T) {
	runner := func(_ context.Context, _ target.SubAgentRequest) (target.SubAgentResult, error) {
		return target.SubAgentResult{}, nil
	}
	ctx := target.WithSubAgent(context.Background(), runner)
	sys := System{}

	// Without cwd or without an assignment: a clear refusal instead of a run into the void.
	if _, err := sys.Execute(ctx, "agent", json.RawMessage(`{"task":"x"}`), target.Credential{}); err == nil {
		t.Fatal("agent without cwd must fail")
	}
	if _, err := sys.Execute(ctx, "agent", json.RawMessage(`{"cwd":"repos/p1"}`), target.Credential{}); err == nil {
		t.Fatal("agent without task must fail")
	}
	// Without a runner in the context (control-plane context) there is no runtime
	// that could be nested.
	if _, err := sys.Execute(context.Background(), "agent",
		json.RawMessage(`{"cwd":"repos/p1","task":"x"}`), target.Credential{}); err == nil {
		t.Fatal("agent without a runner must fail")
	}
}

// groupFinished waits until no running process is left in the process group. A
// fixed sleep is not enough for that: the supervisor waits for the main process
// (the shell), but the group contains its children — on a busy runner the
// kernel takes longer to clear them away than the test waits. That is exactly
// what made the test fail in CI while it was green on a developer machine.
//
// A finished but not yet reaped process (zombie) does not count as running — it
// is dead, only its entry lives on. kill(0) cannot tell the difference, hence
// the look at the process state.
func groupFinished(pgid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if syscall.Kill(-pgid, syscall.Signal(0)) != nil {
			return true // the group no longer exists
		}
		if !groupHasLive(pgid) {
			return true // only zombies left
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// groupHasLive reports whether the process group contains at least one process
// that is not in state Z (zombie) or X (dead).
func groupHasLive(pgid int) bool {
	if entries, err := os.ReadDir("/proc"); err == nil {
		for _, e := range entries {
			if _, err := strconv.Atoi(e.Name()); err != nil {
				continue
			}
			raw, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if err != nil {
				continue
			}
			// Format: pid (comm) state ppid pgrp … — comm may contain spaces and
			// parentheses, so split after the last ')'.
			line := string(raw)
			i := strings.LastIndexByte(line, ')')
			if i < 0 {
				continue
			}
			f := strings.Fields(line[i+1:])
			if len(f) < 4 {
				continue
			}
			if f[3] == strconv.Itoa(pgid) && f[0] != "Z" && f[0] != "X" {
				return true
			}
		}
		return false
	}
	// No /proc (macOS): via ps.
	out, err := exec.Command("ps", "-o", "pgid=,stat=", "-ax").Output()
	if err != nil {
		return true // when in doubt count it as alive rather than lying green
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == strconv.Itoa(pgid) && !strings.HasPrefix(f[1], "Z") {
			return true
		}
	}
	return false
}
