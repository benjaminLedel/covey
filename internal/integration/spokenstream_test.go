package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/llm"
)

// sprechendesModell is a triage that streams (#529): its first sentence is
// handed over, then it waits for weiter before it writes the rest.
type sprechendesModell struct{ weiter chan struct{} }

func (sprechendesModell) Name() string { return "test" }

const sprechendeAntwort = `{"action":"answer","spoken":"Ja, ist drin. Die Tests sind grün.","text":"Ist drin — MR !88, Pipeline grün."}`

func (sprechendesModell) Complete(context.Context, llm.Request) (string, error) {
	return sprechendeAntwort, nil
}

func (m sprechendesModell) Stream(ctx context.Context, _ llm.Request, onText func(string)) (string, error) {
	onText(`{"action":"answer","spoken":"Ja, ist drin. D`)
	select {
	case <-m.weiter:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	onText(`ie Tests sind grün.","text":"Ist drin — MR !88, Pipeline grün."}`)
	return sprechendeAntwort, nil
}

// TestACallHearsTheReplyWhileItIsWritten is #529: the reply to a message
// said in a call streams its spoken sentences to the caller before the
// model has finished; the message follows as ever. A typed message has no
// stream, and nobody outside the conversation reads one.
func TestACallHearsTheReplyWhileItIsWritten(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("strom")
	s.ohneLaeufe(agent.ID)
	modell := sprechendesModell{weiter: make(chan struct{})}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	conv := "/api/v1/conversations/" + c["id"].(string)

	out := admin.expect(http.MethodPost, conv+"/messages", map[string]any{
		"text": "Ist der Merge Request drin?", "meta": map[string]any{"via": "call"}}, http.StatusAccepted)
	id := out["message"].(map[string]any)["id"].(string)

	resp := admin.do(http.MethodGet, conv+"/messages/"+id+"/spoken", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("HTTP %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(resp.Body)
	next := func() map[string]any {
		t.Helper()
		if !lines.Scan() {
			t.Fatalf("the stream ended: %v", lines.Err())
		}
		var v map[string]any
		if err := json.Unmarshal(lines.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if first := next(); first["text"] != "Ja, ist drin." {
		t.Fatalf("first: %v", first)
	}
	// Heard before the model has written the rest, and before the message.
	msgID, _ := uuid.Parse(id)
	var n int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE reply_to = $1`, msgID).Scan(&n)
	if n != 0 {
		t.Fatal("the reply was written before the model finished")
	}
	close(modell.weiter)
	if second := next(); second["text"] != "Die Tests sind grün." {
		t.Fatalf("second: %v", second)
	}
	if end := next(); end["end"] != true {
		t.Fatalf("the spoken form's end (#533): %v", end)
	}
	if done := next(); done["done"] != true {
		t.Fatalf("end: %v", done)
	}
	_, meta := antwortAuf(t, s, msgID)
	if meta[chat.MetaSpoken] != "Ja, ist drin. Die Tests sind grün." {
		t.Fatalf("the message keeps its spoken form: %v", meta)
	}

	// A typed message: nothing to stream.
	typed := admin.expect(http.MethodPost, conv+"/messages", map[string]any{"text": "Und sonst?"}, http.StatusAccepted)
	r := admin.do(http.MethodGet, conv+"/messages/"+typed["message"].(map[string]any)["id"].(string)+"/spoken", nil)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("a typed message streams: HTTP %d", r.StatusCode)
	}
	// Another conversation's path to the same message: as if it did not exist.
	other := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": s.newSupportAgent("strom-2").ID}}, http.StatusCreated)
	r = admin.do(http.MethodGet, "/api/v1/conversations/"+other["id"].(string)+"/messages/"+id+"/spoken", nil)
	r.Body.Close()
	if r.StatusCode != http.StatusNotFound {
		t.Fatalf("read through another conversation: HTTP %d", r.StatusCode)
	}
}
