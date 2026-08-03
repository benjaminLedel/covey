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
		t.Fatalf("output muss echo und workdir (cwd-default) enthalten: %q", out)
	}
}

func TestExecFailureIsResultNotError(t *testing.T) {
	res := execute(t, t.TempDir(), "exec", `{"cmd":"echo kaputt >&2; exit 3"}`).(map[string]any)
	if res["exit_code"] != 3 {
		t.Fatalf("exit-code muss als ergebnis kommen: %+v", res)
	}
	if !strings.Contains(res["output"].(string), "kaputt") {
		t.Fatalf("stderr muss im output landen: %+v", res)
	}
}

func TestExecTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	res := execute(t, t.TempDir(), "exec", `{"cmd":"sleep 30","timeout_secs":1}`).(map[string]any)
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout hat nicht gegriffen")
	}
	if res["exit_code"] != -1 || !strings.Contains(res["error"].(string), "timeout") {
		t.Fatalf("timeout muss als fehler-ergebnis kommen: %+v", res)
	}
}

func TestExecRelativeCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := execute(t, dir, "exec", `{"cmd":"pwd","cwd":"sub"}`).(map[string]any)
	if !strings.Contains(res["output"].(string), filepath.Join(dir, "sub")) {
		t.Fatalf("cwd muss relativ zum workdir aufgelöst werden: %+v", res)
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

	// Doppelstart desselben Namens muss abgelehnt werden.
	ctx := target.WithWorkdir(context.Background(), dir)
	if _, err := (System{}).Execute(ctx, "start",
		json.RawMessage(`{"name":"ticker","cmd":"true"}`), target.Credential{}); err == nil {
		t.Fatal("doppelstart muss fehlschlagen")
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
	// Die ganze Prozessgruppe muss weg sein.
	if !groupFinished(pid) {
		t.Fatal("prozessgruppe lebt nach stop noch")
	}

	// Nach dem Stop darf derselbe Name neu starten.
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
		t.Fatal("shutdown muss alle prozessgruppen beenden")
	}
	list := execute(t, dir, "list", `{}`).([]map[string]any)
	for _, p := range list {
		if p["name"] == "langlaeufer" && p["running"] == true {
			t.Fatalf("prozess läuft nach shutdown noch: %+v", p)
		}
	}
}

func TestParamValidation(t *testing.T) {
	ctx := target.WithWorkdir(context.Background(), t.TempDir())
	for name, call := range map[string][2]string{
		"exec ohne cmd":     {"exec", `{}`},
		"start ohne name":   {"start", `{"cmd":"true"}`},
		"start ohne cmd":    {"start", `{"name":"x"}`},
		"stop unbekannt":    {"stop", `{"name":"gibtsnicht"}`},
		"logs unbekannt":    {"logs", `{"name":"gibtsnicht"}`},
		"unbekannte aktion": {"quatsch", `{}`},
	} {
		if _, err := (System{}).Execute(ctx, call[0], json.RawMessage(call[1]), target.Credential{}); err == nil {
			t.Fatalf("%s muss fehlschlagen", name)
		}
	}
}

func TestActionSubjectAndDescriptor(t *testing.T) {
	if got := (System{}).ActionSubject("start", nil); got != "dev:start" {
		t.Fatalf("subject: %s", got)
	}
	d, ok := target.Describe("dev")
	if !ok || !d.NoCredentials {
		t.Fatal("dev muss registriert und NoCredentials sein")
	}
	if _, err := (System{}).ParseWebhook(nil); err == nil {
		t.Fatal("dev hat keinen webhook")
	}
}

func TestLogBufferCapsAndTails(t *testing.T) {
	b := &logBuffer{}
	for range 3 {
		b.Write(make([]byte, maxLogBytes))
	}
	if got, truncated := b.Tail(0); !truncated || len(got) > maxLogBytes {
		t.Fatalf("puffer muss kappen: len=%d truncated=%v", len(got), truncated)
	}
	b = &logBuffer{}
	b.Write([]byte("a\nb\nc\n"))
	if got, _ := b.Tail(2); got != "b\nc" {
		t.Fatalf("tail: %q", got)
	}
}

// TestSubAgentAction deckt die Übergabe der Programmierarbeit an einen
// Sub-Agenten im Projekt-Checkout ab: Das Plugin selbst fährt keinen Lauf, es
// reicht den Auftrag an den Runner durch, den der Daemon in den Context hängt.
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
		t.Fatalf("auftrag falsch durchgereicht: %+v", got)
	}
	out := res.(target.SubAgentResult)
	if out.Result != "Fix erledigt" || len(out.ChangedFiles) != 1 {
		t.Fatalf("ergebnis falsch: %+v", out)
	}
}

func TestSubAgentActionValidation(t *testing.T) {
	runner := func(_ context.Context, _ target.SubAgentRequest) (target.SubAgentResult, error) {
		return target.SubAgentResult{}, nil
	}
	ctx := target.WithSubAgent(context.Background(), runner)
	sys := System{}

	// Ohne cwd bzw. ohne Auftrag: klare Ablehnung statt eines Laufs ins Leere.
	if _, err := sys.Execute(ctx, "agent", json.RawMessage(`{"task":"x"}`), target.Credential{}); err == nil {
		t.Fatal("agent ohne cwd muss fehlschlagen")
	}
	if _, err := sys.Execute(ctx, "agent", json.RawMessage(`{"cwd":"repos/p1"}`), target.Credential{}); err == nil {
		t.Fatal("agent ohne task muss fehlschlagen")
	}
	// Ohne Runner im Context (Control-Plane-Kontext) gibt es keine Runtime,
	// die sich schachteln ließe.
	if _, err := sys.Execute(context.Background(), "agent",
		json.RawMessage(`{"cwd":"repos/p1","task":"x"}`), target.Credential{}); err == nil {
		t.Fatal("agent ohne Runner muss fehlschlagen")
	}
}

// groupFinished wartet, bis in der Prozessgruppe kein laufender Prozess mehr
// steckt. Ein fester Sleep reicht dafuer nicht: der Supervisor wartet auf den
// Hauptprozess (die Shell), aber die Gruppe enthaelt deren Kinder — auf einem
// ausgelasteten Runner braucht der Kernel laenger, sie abzuraeumen, als der
// Test wartet. Genau daran ist der Test in der CI gescheitert, waehrend er auf
// einer Entwicklermaschine gruen war.
//
// Ein beendeter, aber noch nicht abgeholter Prozess (Zombie) zaehlt nicht als
// laufend — er ist tot, nur seine Eintragung lebt noch. kill(0) kann das nicht
// unterscheiden, deshalb der Blick in den Prozesszustand.
func groupFinished(pgid int) bool {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if syscall.Kill(-pgid, syscall.Signal(0)) != nil {
			return true // die Gruppe gibt es nicht mehr
		}
		if !groupHasLive(pgid) {
			return true // nur noch Zombies
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// groupHasLive meldet, ob die Prozessgruppe mindestens einen Prozess enthaelt,
// der nicht im Zustand Z (zombie) oder X (tot) ist.
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
			// Format: pid (comm) state ppid pgrp … — comm darf Leerzeichen und
			// Klammern enthalten, deshalb hinter der letzten ')' trennen.
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
	// Kein /proc (macOS): ueber ps.
	out, err := exec.Command("ps", "-o", "pgid=,stat=", "-ax").Output()
	if err != nil {
		return true // im Zweifel als lebend werten, statt gruen zu luegen
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == strconv.Itoa(pgid) && !strings.HasPrefix(f[1], "Z") {
			return true
		}
	}
	return false
}
