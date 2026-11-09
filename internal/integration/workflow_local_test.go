package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWorkflowStepDoesNotRunLocalSystems is #698: a workflow's action step
// called the plugin's Execute in the control plane's own process, so dev:exec
// in a step ran a shell on the control plane's host, outside every sandbox.
func TestWorkflowStepDoesNotRunLocalSystems(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/dev", map[string]any{"enabled": true}, http.StatusOK)
	agent, err := s.registry.Create(ctx, s.orgID, "coder", "Coding-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md": "# Coding-Agent", "ACCESS.md": "- system: dev scope: exec",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	proof := filepath.Join(t.TempDir(), "proof.txt")
	params, _ := json.Marshal(map[string]string{"cmd": "echo ran > " + proof})

	_, err = s.orch.ActAs(ctx, agent, "dev", "exec", params, "")
	if err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("a workflow step on dev has to be refused, got %v", err)
	}
	if _, statErr := os.Stat(proof); !os.IsNotExist(statErr) {
		t.Fatal("the command ran in the control plane's process")
	}
}
