package egress

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The API resolver is what stands between an isolated sandbox and the internet.
// Its interesting behaviour is not the happy path but what it does when the
// control plane does NOT answer as expected: a proxy that lets something
// through because it could not ask is worse than one that blocks too much.

type stubControlPlane struct {
	mu        sync.Mutex
	calls     int
	decisions []Decision
	status    int
	answer    AllowlistResponse
}

func (c *stubControlPlane) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/runner/v1/egress/allowlist", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runner-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		c.mu.Lock()
		c.calls++
		status, answer := c.status, c.answer
		c.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		writeJSONTest(w, answer)
	})
	mux.HandleFunc("POST /api/runner/v1/egress/decisions", func(w http.ResponseWriter, r *http.Request) {
		var req DecisionsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		c.mu.Lock()
		c.decisions = append(c.decisions, req.Decisions...)
		c.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func writeJSONTest(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestAPIResolverAllowsOnlyWhatTheControlPlaneNames(t *testing.T) {
	agent := uuid.New()
	cp := &stubControlPlane{answer: AllowlistResponse{
		Patterns:  []string{"zammad.example.com"},
		TokenHash: HashToken("sandbox-token"),
	}}
	srv := httptest.NewServer(cp.handler(t))
	defer srv.Close()

	r := NewAPIResolver(context.Background(), srv.URL, "runner-token",
		[]string{"host.docker.internal"}, time.Minute, nil)

	allow, id, ok := r.Resolve(context.Background(), agent.String(), "sandbox-token")
	if !ok {
		t.Fatal("with a correct token the resolve has to succeed")
	}
	if id != agent {
		t.Errorf("agent %v, expected %v", id, agent)
	}
	if !allow.Allows("zammad.example.com") {
		t.Error("the host named by the control plane has to be allowed")
	}
	// The ENV defaults travel along — without them the sandbox would not even
	// reach the control plane in hard isolation mode.
	if !allow.Allows("host.docker.internal") {
		t.Error("the defaults from the environment belong in the allowlist")
	}
	if allow.Allows("irgendwas.example.com") {
		t.Error("everything unnamed stays blocked")
	}

	// A wrong per-sandbox token is rejected, and the answer stays cached: the
	// second call must not produce a fresh request per attempt.
	if _, _, ok := r.Resolve(context.Background(), agent.String(), "falsch"); ok {
		t.Error("a wrong per-sandbox token has to be rejected")
	}
	cp.mu.Lock()
	calls := cp.calls
	cp.mu.Unlock()
	if calls != 1 {
		t.Errorf("within the TTL one request suffices, got %d", calls)
	}
}

func TestAPIResolverFailsClosed(t *testing.T) {
	agent := uuid.New()

	// The control plane refuses the runner token — a runner whose token has been
	// revoked must not keep working with what it last knew.
	cp := &stubControlPlane{status: http.StatusUnauthorized}
	srv := httptest.NewServer(cp.handler(t))
	defer srv.Close()
	r := NewAPIResolver(context.Background(), srv.URL, "falscher-token", nil, time.Minute, nil)
	if _, _, ok := r.Resolve(context.Background(), agent.String(), "egal"); ok {
		t.Error("without a valid runner token nothing may pass")
	}

	// The control plane is not there at all. This is the case that decides
	// whether an outage becomes an open door: the proxy container starts before
	// it is attached to the bridge network, so the first requests run into
	// exactly this.
	tot := NewAPIResolver(context.Background(), "http://127.0.0.1:1", "runner-token", nil, time.Minute, nil)
	if _, _, ok := tot.Resolve(context.Background(), agent.String(), "egal"); ok {
		t.Error("an unreachable control plane has to block, not let through")
	}
}

func TestAPIResolverBatchesItsDecisions(t *testing.T) {
	agent := uuid.New()
	cp := &stubControlPlane{answer: AllowlistResponse{TokenHash: HashToken("x")}}
	srv := httptest.NewServer(cp.handler(t))
	defer srv.Close()

	r := NewAPIResolver(context.Background(), srv.URL, "runner-token", nil, time.Minute, nil)
	for i := 0; i < 20; i++ {
		r.Log(agent, "host.example.com", "GET", i%2 == 0)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		cp.mu.Lock()
		n := len(cp.decisions)
		cp.mu.Unlock()
		if n == 20 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of 20 decisions arrived", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
