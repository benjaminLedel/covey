package runner

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A handshake that names another runner's id is refused BEFORE it touches the
// pool. It used to be admitted first and checked second, and the detach that
// followed the refusal removed what had just been inserted — the victim's own
// live entry, since both went under the same key (#159).
func TestAForeignIdentityIsRefusedWithoutEvictingItsOwner(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	_, victim, _ := registriereFalschenRunner(t, p, orgID)

	// A second connection, authenticated as another runner of the same
	// organisation, claims the victim's id.
	impostorToken := uuid.New()
	control, nodeEnd := NewInProc()
	reg, _ := encode(TypeRegistered, "", Registered{RunnerID: victim, OrgID: orgID, Protocol: Protocol})
	ctx := context.Background()
	if err := nodeEnd.Send(ctx, reg); err != nil {
		t.Fatal(err)
	}
	err := p.AttachRemote(ctx, control, impostorToken, orgID, nil)
	if err == nil {
		t.Fatal("a handshake naming a foreign identity has to be refused")
	}

	c := p.connFor(orgID, victim)
	if c == nil {
		t.Fatal("the refused handshake evicted the runner whose id it claimed")
	}
	select {
	case <-c.gone:
		t.Fatal("the victim's connection was ended by the refused handshake")
	default:
	}
}

// A runner reporting on an agent that runs on ANOTHER host is telling a story
// about somebody else's sandbox. sandbox_exited from it must not end that
// sandbox's wake (#159).
func TestASandboxExitIsOnlyTakenFromTheHostThatRunsIt(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	died := make(chan uuid.UUID, 1)
	p.SandboxDied = func(agentID uuid.UUID, _ string) { died <- agentID }

	// Two runners; the agent is placed on the first.
	_, host, _ := registriereFalschenRunner(t, p, orgID)
	other, _, _ := registriereFalschenRunner(t, p, orgID)
	agentID := uuid.New()
	p.mu.Lock()
	p.placed[agentID] = host
	p.mu.Unlock()

	exit, _ := encode(TypeSandboxExited, "", SandboxExited{AgentID: agentID, Reason: "made up"})
	if err := other.Send(context.Background(), exit); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-died:
		t.Fatalf("a report from a host the agent does not run on ended its wake: %s", id)
	case <-time.After(300 * time.Millisecond):
	}
}
