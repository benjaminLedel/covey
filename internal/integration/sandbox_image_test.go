package integration

import (
	"context"
	"net/http"
	"testing"
)

// The sandbox image hangs off the agent, not off the instance (D11, spec/16).
// What has to hold: the value survives the round trip through the API, and an
// upgrade does not silently move an existing agent into a different workplace —
// migration 0052 puts everyone on `dev`, because that is what the old
// instance-wide image contained. An agent that quietly loses its toolchain
// fails at the first `composer install`, and the reason would be nowhere near
// the symptom.

func TestSandboxImageIsAnAgentProperty(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("workplace-agent")

	// Migration 0052: existing agents keep what they had. The test agent is
	// created after it, so it gets the column default — what matters here is
	// that the field exists and comes back through the API.
	if _, err := s.pool.Exec(ctx, "UPDATE agents SET sandbox_image = 'dev' WHERE id = $1", agent.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.SandboxImage != "dev" {
		t.Fatalf("workplace %q, expected dev", reloaded.SandboxImage)
	}

	c := login(t, s, "admin@test.local", "admin-passwort")
	set := func(value string) int {
		resp := c.do(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/sandbox-image",
			map[string]string{"sandbox_image": value})
		resp.Body.Close()
		return resp.StatusCode
	}

	if status := set("base"); status != http.StatusOK {
		t.Fatalf("setting the workplace: status %d", status)
	}
	if a, _ := s.registry.Get(ctx, agent.ID); a.SandboxImage != "base" {
		t.Errorf("workplace after the change: %q", a.SandboxImage)
	}

	// An organisation's own image is a valid value — the third row of the
	// profile table. A validation against a list of profiles would have to know
	// every image an organisation builds for itself.
	if status := set("  registry.example.com/team/sandbox:2026-08  "); status != http.StatusOK {
		t.Fatalf("own image: status %d", status)
	}
	a, _ := s.registry.Get(ctx, agent.ID)
	if a.SandboxImage != "registry.example.com/team/sandbox:2026-08" {
		t.Errorf("own image was not stored trimmed: %q", a.SandboxImage)
	}

	// The startup check follows the agents: it has to see this image, so that a
	// missing one is reported before the first wake runs into it.
	inUse, err := s.registry.SandboxImagesInUse(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inUse["registry.example.com/team/sandbox:2026-08"] != 1 {
		t.Errorf("the workplace in use has to show up in the check: %v", inUse)
	}
}
