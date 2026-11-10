package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/msauth"
	"covey/internal/orchestrator"
	"covey/internal/secrets/sealbox"
	"covey/internal/teamsuser"
	"covey/internal/teamsuser/usersys"
)

// Teams as a person (#712): a person's delegated token works only for the
// agent it was granted to, stays sealed at rest, survives Microsoft's
// refresh-token rotation, and every message says the agent sent it.

type fakeMicrosoft struct {
	srv      *httptest.Server
	mu       sync.Mutex
	nonce    string
	refresh  string // the refresh token currently valid
	issued   int
	graph    []string // "METHOD path auth body"
	refreshN int
}

func newFakeMicrosoft(t *testing.T) *fakeMicrosoft {
	f := &fakeMicrosoft{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/oauth2/v2.0/token") {
			_ = r.ParseForm()
			switch r.Form.Get("grant_type") {
			case "authorization_code":
				if r.Form.Get("code") != "the-code" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			case "refresh_token":
				if r.Form.Get("refresh_token") != f.refresh {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error_description":"refresh token unknown or used"}`))
					return
				}
				f.refreshN++
			}
			f.issued++
			f.refresh = "refresh-" + string(rune('0'+f.issued))
			claims, _ := json.Marshal(map[string]any{"aud": "user-app", "tid": "tenant-a", "nonce": f.nonce,
				"preferred_username": "ada@example.org"})
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "access-" + string(rune('0'+f.issued)), "refresh_token": f.refresh, "expires_in": 3600,
				"id_token": "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".sig",
			})
			return
		}
		body, _ := io.ReadAll(r.Body)
		f.graph = append(f.graph, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(body))
		switch {
		case r.URL.Path == "/v1.0/me/chats":
			_, _ = w.Write([]byte(`{"value":[{"id":"19:chat","topic":"Budget","chatType":"group","members":[{"displayName":"Ada"},{"displayName":"Bo"}]}]}`))
		case strings.HasSuffix(r.URL.Path, "/messages") && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`{"value":[{"id":"m1","messageType":"message","createdDateTime":"2026-11-10T09:00:00Z","from":{"user":{"displayName":"Bo"}},"body":{"content":"<p>Numbers &amp; dates?</p>"}}]}`))
		default:
			_, _ = w.Write([]byte(`{"id":"sent-1"}`))
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeMicrosoft) graphCalls(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.graph {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

func newUserService(t *testing.T, s *stack, ms *fakeMicrosoft, box *sealbox.Box) *teamsuser.Service {
	svc := teamsuser.New(s.pool, box, teamsuser.Config{AppID: "user-app", AppPassword: "pw",
		LoginBase: ms.srv.URL, GraphBase: ms.srv.URL + "/v1.0"}, nil)
	if svc == nil {
		t.Fatal("service not built")
	}
	return svc
}

func delegate(t *testing.T, svc *teamsuser.Service, ms *fakeMicrosoft, org, agent, human uuid.UUID) {
	t.Helper()
	u, nonce, err := svc.Start(org, agent, human, "https://covey.example.org/api/teamsuser/callback")
	if err != nil {
		t.Fatal(err)
	}
	ms.mu.Lock()
	ms.nonce = nonce
	ms.mu.Unlock()
	pu, _ := url.Parse(u)
	if !strings.Contains(pu.Query().Get("scope"), "offline_access") {
		t.Fatalf("the grant asks for no refresh token: %s", pu.Query().Get("scope"))
	}
	if _, _, err := svc.Finish(context.Background(), pu.Query().Get("state"), nonce, "the-code", "cb"); err != nil {
		t.Fatal(err)
	}
}

func TestTeamsUserDelegationStaysWithItsAgent(t *testing.T) {
	usersys.Register()
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	ms := newFakeMicrosoft(t)
	var svc *teamsuser.Service
	ts := newTeamsAppStackWith(t, func(s *stack, m map[string]orchestrator.PlatformSystem) {
		svc = newUserService(t, s, ms, box)
		m[usersys.Name] = svc
	})
	ctx := context.Background()
	if err := ts.targets.SetEnabled(ctx, ts.orgID, usersys.Name, true); err != nil {
		t.Fatal(err)
	}
	vera, _ := ts.registry.Create(ctx, ts.orgID, "vera", "Vera", "mock", nil)
	_, _ = ts.registry.SaveConfig(ctx, vera.ID, map[string]string{"SOUL.md": "# Vera\n\n## Role\nAssist.", "ACCESS.md": "- system: teamsuser scope: read,write"}, nil)
	paula, _ := ts.registry.Create(ctx, ts.orgID, "paula", "Paula", "mock", nil)
	ts.ohneLaeufe(paula.ID)

	// A sign-in finished in another browser binds nothing.
	u, nonce, _ := svc.Start(ts.orgID, vera.ID, ts.adminID, "cb")
	pu, _ := url.Parse(u)
	ms.nonce = nonce
	if _, _, err := svc.Finish(ctx, pu.Query().Get("state"), "other-browser", "the-code", "cb"); !errors.Is(err, msauth.ErrState) {
		t.Fatalf("expected the browser binding to refuse: %v", err)
	}

	delegate(t, svc, ms, ts.orgID, vera.ID, ts.adminID)

	// Sealed at rest: the refresh token is nowhere in the row in the clear.
	var ct []byte
	_ = ts.pool.QueryRow(ctx, `SELECT ciphertext FROM teamsuser_delegations WHERE agent_id = $1`, vera.ID).Scan(&ct)
	if len(ct) == 0 || strings.Contains(string(ct), "refresh-") {
		t.Fatal("the refresh token is not sealed")
	}

	// Vera reads and writes with Ada's token; what she sends names her.
	out, err := svc.Execute(ctx, vera, uuid.Nil, "read_chat", json.RawMessage(`{"chat_id":"19:chat"}`))
	if err != nil {
		t.Fatal(err)
	}
	if msgs, _ := out.([]map[string]string); len(msgs) != 1 || msgs[0]["text"] != "Numbers & dates?" || msgs[0]["from"] != "Bo" {
		t.Fatalf("messages not read as text: %#v", out)
	}
	if _, err := svc.Execute(ctx, vera, uuid.Nil, "send_chat", json.RawMessage(`{"chat_id":"19:chat","text":"Here <b>they</b> are"}`)); err != nil {
		t.Fatal(err)
	}
	sent := ms.graphCalls("POST /v1.0/chats/19:chat/messages")
	if len(sent) != 1 || !strings.Contains(sent[0], "Bearer access-1") || !strings.Contains(sent[0], "sent by Vera (covey)") ||
		strings.Contains(sent[0], "<b>they</b>") {
		t.Fatalf("send: %v", sent)
	}

	// Paula, same organisation, gets nothing of Ada's; nor does an agent of
	// another organisation that names Vera's chat.
	if _, err := svc.Execute(ctx, paula, uuid.Nil, "read_chat", json.RawMessage(`{"chat_id":"19:chat"}`)); !errors.Is(err, teamsuser.ErrNoDelegation) {
		t.Fatalf("another agent used the delegation: %v", err)
	}
	stranger := vera
	stranger.OrgID = uuid.New()
	if _, err := svc.Execute(ctx, stranger, uuid.Nil, "list_chats", nil); !errors.Is(err, teamsuser.ErrNoDelegation) {
		t.Fatalf("the delegation answered outside its organisation: %v", err)
	}

	// A fresh process refreshes, and stores the rotated refresh token: two
	// restarts in a row both still work.
	for i := 0; i < 2; i++ {
		fresh := newUserService(t, ts.stack, ms, box)
		if _, err := fresh.Execute(ctx, vera, uuid.Nil, "list_chats", nil); err != nil {
			t.Fatalf("after restart %d: %v", i+1, err)
		}
	}
	if ms.refreshN != 2 {
		t.Fatalf("expected two refreshes, got %d", ms.refreshN)
	}

	// Through the sandbox: the agent's action reaches Graph via the control
	// plane, the token never leaves it.
	if _, err := ts.backlog.Create(ctx, ts.orgID, vera.ID, "answer",
		`[mock:action teamsuser/send_chat {"chat_id":"19:chat","text":"From the run"}]`+"\n[mock:result Done]", "test", 3); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the run's message reaches Graph", 20*time.Second, func() bool {
		for _, c := range ms.graphCalls("POST /v1.0/chats/19:chat/messages") {
			if strings.Contains(c, "From the run") {
				return true
			}
		}
		return false
	})

	// Revoked: gone, and the agent says so instead of acting as anyone else.
	if err := svc.Revoke(ctx, ts.orgID, vera.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, vera, uuid.Nil, "list_chats", nil); !errors.Is(err, teamsuser.ErrNoDelegation) {
		t.Fatalf("still acting after revoke: %v", err)
	}
}
