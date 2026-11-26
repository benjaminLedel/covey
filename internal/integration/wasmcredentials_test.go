package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"covey/internal/agents"
	"covey/internal/backlog"
)

// buildDemoWasmDeclaring builds the fixture plugin with a credentials
// declaration linked in, the way buildDemoWasm builds it without one.
func buildDemoWasmDeclaring(t *testing.T, credentials string) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "demo.wasm")
	cmd := exec.Command("go", "build", "-ldflags=-X main.credentials="+credentials, "-o", out, ".")
	cmd.Dir = "../target/wasmplug/testdata/demoplugin"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the wasm fixture: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// installDemoWasm stores the module as a catalogue install would, switches it
// on and gives an agent access to it — everything except a secret.
func installDemoWasm(t *testing.T, s *stack, module []byte, slug string) agents.Agent {
	t.Helper()
	ctx := context.Background()
	sum := sha256.Sum256(module)
	if _, err := s.targets.PutFromCatalog(ctx, s.orgID, "wasm", module,
		"https://catalog.example/catalog.json", "1.0.0", hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	if err := s.targets.SetEnabled(ctx, s.orgID, "demo", true); err != nil {
		t.Fatal(err)
	}
	agent, err := s.registry.Create(ctx, s.orgID, slug, "Demo user", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Demo user",
		"ACCESS.md": "- system: demo scope: read",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	return agent
}

// setupCredentials reads what the setup assistant asks the organisation to
// store for a system: key → optional.
func setupCredentials(t *testing.T, s *stack, name string) map[string]bool {
	t.Helper()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	resp := admin.do(http.MethodGet, "/api/v1/targets/"+name+"/setup", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: HTTP %d", resp.StatusCode)
	}
	var state struct {
		Credentials []struct {
			Key      string `json:"key"`
			Optional bool   `json:"optional"`
		} `json:"credentials"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, c := range state.Credentials {
		out[c.Key] = c.Optional
	}
	return out
}

// A wasm plugin from the catalogue that reaches only public hosts was denied
// every action for a secret it never needed ("no secret stored or assigned:
// <name>_token"): target.Describe knows only compiled plugins, so the broker
// never saw that the plugin did not need one. A plugin says so now in its
// description, and the broker has to hear it — through the sandbox, the way
// an agent's action arrives.
func TestWasmPluginDeclaringNoCredentialsIsGrantedWithoutASecret(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := installDemoWasm(t, s, buildDemoWasmDeclaring(t, "none"), "public-user")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Use a public plugin",
		`[mock:action demo/shout {"text":"quiet"}]
[mock:result done]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task terminal", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if st := s.taskState(task.ID); st != backlog.StateDone {
		got, _ := s.backlog.Get(ctx, task.ID)
		t.Fatalf("task = %s (%v), want done — no secret is stored and none is needed", st, got.Error)
	}
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT count(*) > 0 FROM recording_events
		WHERE agent_id=$1 AND kind='action' AND payload->>'action'='demo:shout' AND (payload->>'ok')::bool`,
		agent.ID).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the action was not recorded as having run")
	}
	var denied int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='credential' AND payload->>'system'='demo'
		  AND NOT (payload->>'granted')::bool`, agent.ID).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if denied != 0 {
		t.Errorf("%d credential denials recorded for a plugin that needs none", denied)
	}
}

// The broker decision for all three declarations, through the one entrance
// that returns it directly (a workflow step, ActAs). "optional" passes on what
// is stored; no declaration stays what it always was — denied without a
// token, and denied without a URL once a token is there.
func TestWasmPluginCredentialDeclarationsAtTheBroker(t *testing.T) {
	params := json.RawMessage(`{"text":"quiet"}`)

	t.Run("none", func(t *testing.T) {
		s := newStack(t)
		agent := installDemoWasm(t, s, buildDemoWasmDeclaring(t, "none"), "none-user")
		if _, err := s.orch.ActAs(context.Background(), agent, "demo", "shout", params, ""); err != nil {
			t.Fatalf("credentials none has to be granted without a secret: %v", err)
		}
		// The setup assistant asks for nothing either.
		if creds := setupCredentials(t, s, "demo"); len(creds) != 0 {
			t.Errorf("setup asks for %v from a plugin that needs no credentials", creds)
		}
	})

	t.Run("optional", func(t *testing.T) {
		s := newStack(t)
		ctx := context.Background()
		agent := installDemoWasm(t, s, buildDemoWasmDeclaring(t, "optional"), "optional-user")
		if _, err := s.orch.ActAs(ctx, agent, "demo", "shout", params, ""); err != nil {
			t.Fatalf("credentials optional has to be granted without a secret: %v", err)
		}
		if creds := setupCredentials(t, s, "demo"); len(creds) != 2 || !creds["demo_token"] || !creds["demo_url"] {
			t.Errorf("setup has to offer both secrets as optional, got %v", creds)
		}
		// A stored token is passed on — the recording says the grant was
		// authenticated — and a missing URL still does not deny.
		if err := s.secrets.Put(ctx, s.orgID, "demo_token", "s3cret"); err != nil {
			t.Fatal(err)
		}
		if err := s.secrets.Assign(ctx, s.orgID, "demo_token", agent.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.orch.ActAs(ctx, agent, "demo", "shout", params, ""); err != nil {
			t.Fatalf("credentials optional with a token but no URL: %v", err)
		}
		var authenticated bool
		if err := s.pool.QueryRow(ctx, `SELECT count(*) > 0 FROM recording_events
			WHERE agent_id=$1 AND kind='credential' AND payload->>'system'='demo'
			  AND (payload->>'granted')::bool AND (payload->>'optional')::bool
			  AND (payload->>'authenticated')::bool`, agent.ID).Scan(&authenticated); err != nil {
			t.Fatal(err)
		}
		if !authenticated {
			t.Error("the stored token was not passed on with the optional grant")
		}
	})

	t.Run("undeclared", func(t *testing.T) {
		s := newStack(t)
		ctx := context.Background()
		agent := installDemoWasm(t, s, buildDemoWasmDeclaring(t, ""), "required-user")
		_, err := s.orch.ActAs(ctx, agent, "demo", "shout", params, "")
		if err == nil || !strings.Contains(err.Error(), "no secret stored or assigned: demo_token") {
			t.Fatalf("a plugin that declares nothing still needs its token, got %v", err)
		}
		if creds := setupCredentials(t, s, "demo"); len(creds) != 2 || creds["demo_token"] || creds["demo_url"] {
			t.Errorf("setup has to ask for both secrets as required, got %v", creds)
		}
		if err := s.secrets.Put(ctx, s.orgID, "demo_token", "s3cret"); err != nil {
			t.Fatal(err)
		}
		if err := s.secrets.Assign(ctx, s.orgID, "demo_token", agent.ID); err != nil {
			t.Fatal(err)
		}
		_, err = s.orch.ActAs(ctx, agent, "demo", "shout", params, "")
		if err == nil || !strings.Contains(err.Error(), "no secret stored or assigned: demo_url") {
			t.Fatalf("a plugin that declares nothing still needs its URL, got %v", err)
		}
	})
}
