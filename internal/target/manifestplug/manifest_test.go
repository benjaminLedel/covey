package manifestplug

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const demoManifest = `{
  "name": "helpdesk",
  "label": "Helpdesk",
  "auth": {"header": "X-API-Key", "format": "{token}"},
  "webhook": {
    "signature": "hmac-sha256",
    "id_field": "issue.id",
    "event_id_field": "comment.id",
    "title_field": "issue.title",
    "body_field": "comment.text",
    "ignore_when": [{"field": "comment.author_type", "equals": "agent"}]
  },
  "actions": {
    "get_issue": {"method": "GET", "path": "/issues/{issue_id}"},
    "comment": {
      "method": "POST", "path": "/issues/{issue_id}/comments",
      "subject_when": [{"param": "public", "equals": true, "subject": "comment_public"}]
    }
  },
  "prompt_doc": "Available helpdesk actions: get_issue, comment."
}`

func mustManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := Parse([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseValidation(t *testing.T) {
	cases := map[string]string{
		"no json":            `{`,
		"name missing":       `{"actions":{"a":{"method":"GET","path":"/x"}},"webhook":{"id_field":"id"}}`,
		"name uppercase":     `{"name":"Bad","actions":{"a":{"method":"GET","path":"/x"}},"webhook":{"id_field":"id"}}`,
		"no actions":         `{"name":"x1","webhook":{"id_field":"id"},"actions":{}}`,
		"bad method":         `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"TRACE","path":"/x"}}}`,
		"path without slash": `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"GET","path":"x"}}}`,
		"id_field missing":   `{"name":"x1","webhook":{},"actions":{"a":{"method":"GET","path":"/x"}}}`,
		"signature unknown":  `{"name":"x1","webhook":{"id_field":"id","signature":"md5"},"actions":{"a":{"method":"GET","path":"/x"}}}`,
		"unknown field":      `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"GET","path":"/x"}},"extra":true}`,
	}
	for name, raw := range cases {
		if _, err := Parse([]byte(raw)); err == nil {
			t.Errorf("%s: error expected", name)
		}
	}
	if _, err := Parse([]byte(demoManifest)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestManifestWebhook(t *testing.T) {
	sys := New(mustManifest(t))

	payload := []byte(`{"issue":{"id":7,"title":"Drucker brennt"},"comment":{"id":3,"text":"Hilfe!","author_type":"customer"}}`)
	ev, err := sys.ParseWebhook(payload)
	if err != nil {
		t.Fatal(err)
	}
	if ev.DedupKey != "helpdesk:7:3" || ev.CorrelationKey != "helpdesk:7" {
		t.Fatalf("wrong keys: %+v", ev)
	}
	if !ev.Wake || !strings.Contains(ev.Title, "Drucker brennt") || !strings.Contains(ev.TaskBody, "Hilfe!") {
		t.Fatalf("wrong event: %+v", ev)
	}

	echo := []byte(`{"issue":{"id":7},"comment":{"id":4,"text":"Antwort","author_type":"agent"}}`)
	ev, err = sys.ParseWebhook(echo)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Wake {
		t.Fatal("the agent echo must not wake (ignore_when)")
	}

	if _, err := sys.ParseWebhook([]byte(`{"comment":{"id":1}}`)); err == nil {
		t.Fatal("a missing id must be an error")
	}
}

func TestManifestVerifyWebhook(t *testing.T) {
	sys := New(mustManifest(t))
	body := []byte(`{"issue":{"id":1}}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := http.Header{}
	h.Set("X-Hub-Signature", sig)
	if !sys.VerifyWebhook("s3cret", body, h) {
		t.Fatal("valid signature rejected")
	}
	h.Set("X-Hub-Signature", "sha256=deadbeef")
	if sys.VerifyWebhook("s3cret", body, h) {
		t.Fatal("invalid signature accepted")
	}
	// No secret configured → verification disabled (dev).
	if !sys.VerifyWebhook("", body, http.Header{}) {
		t.Fatal("without a secret nothing must be verified")
	}
}

func TestManifestActionSubject(t *testing.T) {
	sys := New(mustManifest(t))
	if got := sys.ActionSubject("get_issue", []byte(`{}`)); got != "helpdesk:get_issue" {
		t.Fatalf("wrong default subject: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"public":true}`)); got != "helpdesk:comment_public" {
		t.Fatalf("subject_when does not apply: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"public":false}`)); got != "helpdesk:comment" {
		t.Fatalf("subject_when too broad: %s", got)
	}
}

func TestManifestExecute(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		gotAuth = r.Header.Get("X-API-Key")
		var b strings.Builder
		if r.Body != nil {
			raw := make([]byte, 1024)
			n, _ := r.Body.Read(raw)
			b.Write(raw[:n])
		}
		gotBody = b.String()
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	sys := New(mustManifest(t))
	cred := target.Credential{BaseURL: srv.URL, Token: "tok-123"}

	if _, err := sys.Execute(context.Background(), "get_issue", []byte(`{"issue_id":7}`), cred); err != nil {
		t.Fatal(err)
	}
	if gotPath != "GET /issues/7" || gotAuth != "tok-123" {
		t.Fatalf("wrong request: %s auth=%s", gotPath, gotAuth)
	}

	if _, err := sys.Execute(context.Background(), "comment",
		[]byte(`{"issue_id":7,"text":"Hallo","public":true}`), cred); err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /issues/7/comments" {
		t.Fatalf("wrong path: %s", gotPath)
	}
	// issue_id is consumed in the path — only the remaining params go out as
	// the body.
	if strings.Contains(gotBody, "issue_id") || !strings.Contains(gotBody, "Hallo") {
		t.Fatalf("wrong body: %s", gotBody)
	}

	if _, err := sys.Execute(context.Background(), "get_issue", []byte(`{}`), cred); err == nil {
		t.Fatal("an unresolved placeholder must be an error")
	}
	if _, err := sys.Execute(context.Background(), "kaboom", []byte(`{}`), cred); err == nil {
		t.Fatal("an unknown action must be an error")
	}
}
