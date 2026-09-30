package llm

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"covey/internal/secrets"
)

// namedSecrets answers Get from a map; everything else of the store is
// unused here and would panic, which is the point — Resolve must not reach
// for anything but the two named keys.
type namedSecrets struct {
	secrets.Store
	values map[string]string
}

func (n namedSecrets) Get(_ context.Context, _ uuid.UUID, key string) (string, error) {
	if v, ok := n.values[key]; ok {
		return v, nil
	}
	return "", secrets.ErrNotFound
}

type seatsFunc func(engine string) (string, bool, error)

func (f seatsFunc) ControlPlaneCredential(_ context.Context, _ uuid.UUID, engine string) (string, bool, error) {
	return f(engine)
}

func noSeat(string) (string, bool, error) { return "", false, errors.New("not found") }

// TestResolveFallsBackOnTheSeats is #483: an organisation whose Claude Code
// seat carries its token under a name of its own has agents that run, and has
// to have a control plane that finds the same token.
func TestResolveFallsBackOnTheSeats(t *testing.T) {
	ctx := context.Background()
	org := uuid.New()
	var asked string
	seats := seatsFunc(func(engine string) (string, bool, error) {
		asked = engine
		return "sk-ant-oat01-seat", false, nil
	})
	p, err := Resolve(ctx, namedSecrets{values: map[string]string{}}, seats, org)
	if err != nil {
		t.Fatalf("a seat credential has to be enough: %v", err)
	}
	if asked != "claude-code" {
		t.Fatalf("the seats of the Claude Code engine are asked, got %q", asked)
	}
	a := p.(anthropic)
	if a.cred != "sk-ant-oat01-seat" || !a.oauth {
		t.Fatalf("the token authenticates by its prefix as OAuth, got %+v", a)
	}
}

// TestResolveNamedSecretsComeFirst: the order installations have relied on
// stays — the org secret by its declared name wins over any seat.
func TestResolveNamedSecretsComeFirst(t *testing.T) {
	ctx := context.Background()
	store := namedSecrets{values: map[string]string{"anthropic_api_key": "sk-ant-api03-named"}}
	seats := seatsFunc(func(string) (string, bool, error) {
		t.Fatal("the seats must not be read when a named secret answers")
		return "", false, nil
	})
	p, err := Resolve(ctx, store, seats, uuid.New())
	if err != nil {
		t.Fatal(err)
	}
	if a := p.(anthropic); a.cred != "sk-ant-api03-named" || a.oauth {
		t.Fatalf("got %+v", a)
	}
}

func TestResolveWithoutAnything(t *testing.T) {
	ctx := context.Background()
	if _, err := Resolve(ctx, namedSecrets{values: map[string]string{}}, seatsFunc(noSeat), uuid.New()); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("want ErrNoCredential, got %v", err)
	}
	if _, err := Resolve(ctx, namedSecrets{values: map[string]string{}}, nil, uuid.New()); !errors.Is(err, ErrNoCredential) {
		t.Fatalf("nil seats: want ErrNoCredential, got %v", err)
	}
	if Available(ctx, nil, nil, uuid.New()) {
		t.Fatal("nothing at all is not available")
	}
}
