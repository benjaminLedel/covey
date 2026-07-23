package target

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
  "prompt_doc": "Verfügbare helpdesk-Aktionen: get_issue, comment."
}`

func mustManifest(t *testing.T) Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestParseManifestValidation(t *testing.T) {
	cases := map[string]string{
		"kein json":          `{`,
		"name fehlt":         `{"actions":{"a":{"method":"GET","path":"/x"}},"webhook":{"id_field":"id"}}`,
		"name grossbuchst.":  `{"name":"Bad","actions":{"a":{"method":"GET","path":"/x"}},"webhook":{"id_field":"id"}}`,
		"keine actions":      `{"name":"x1","webhook":{"id_field":"id"},"actions":{}}`,
		"böse methode":       `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"TRACE","path":"/x"}}}`,
		"pfad ohne slash":    `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"GET","path":"x"}}}`,
		"id_field fehlt":     `{"name":"x1","webhook":{},"actions":{"a":{"method":"GET","path":"/x"}}}`,
		"signatur unbekannt": `{"name":"x1","webhook":{"id_field":"id","signature":"md5"},"actions":{"a":{"method":"GET","path":"/x"}}}`,
		"unbekanntes feld":   `{"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"GET","path":"/x"}},"extra":true}`,
	}
	for name, raw := range cases {
		if _, err := ParseManifest([]byte(raw)); err == nil {
			t.Errorf("%s: fehler erwartet", name)
		}
	}
	if _, err := ParseManifest([]byte(demoManifest)); err != nil {
		t.Fatalf("gültiges manifest abgelehnt: %v", err)
	}
}

func TestManifestWebhook(t *testing.T) {
	sys := NewManifestSystem(mustManifest(t))

	payload := []byte(`{"issue":{"id":7,"title":"Drucker brennt"},"comment":{"id":3,"text":"Hilfe!","author_type":"customer"}}`)
	ev, err := sys.ParseWebhook(payload)
	if err != nil {
		t.Fatal(err)
	}
	if ev.DedupKey != "helpdesk:7:3" || ev.CorrelationKey != "helpdesk:7" {
		t.Fatalf("keys falsch: %+v", ev)
	}
	if !ev.Wake || !strings.Contains(ev.Title, "Drucker brennt") || !strings.Contains(ev.TaskBody, "Hilfe!") {
		t.Fatalf("event falsch: %+v", ev)
	}

	echo := []byte(`{"issue":{"id":7},"comment":{"id":4,"text":"Antwort","author_type":"agent"}}`)
	ev, err = sys.ParseWebhook(echo)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Wake {
		t.Fatal("agent-echo darf nicht wecken (ignore_when)")
	}

	if _, err := sys.ParseWebhook([]byte(`{"comment":{"id":1}}`)); err == nil {
		t.Fatal("fehlende id muss ein fehler sein")
	}
}

func TestManifestVerifyWebhook(t *testing.T) {
	sys := NewManifestSystem(mustManifest(t))
	body := []byte(`{"issue":{"id":1}}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	h := http.Header{}
	h.Set("X-Hub-Signature", sig)
	if !sys.VerifyWebhook("s3cret", body, h) {
		t.Fatal("gültige signatur abgelehnt")
	}
	h.Set("X-Hub-Signature", "sha256=deadbeef")
	if sys.VerifyWebhook("s3cret", body, h) {
		t.Fatal("ungültige signatur akzeptiert")
	}
	// Kein Secret konfiguriert → Prüfung deaktiviert (Dev).
	if !sys.VerifyWebhook("", body, http.Header{}) {
		t.Fatal("ohne secret darf nicht geprüft werden")
	}
}

func TestManifestActionSubject(t *testing.T) {
	sys := NewManifestSystem(mustManifest(t))
	if got := sys.ActionSubject("get_issue", []byte(`{}`)); got != "helpdesk:get_issue" {
		t.Fatalf("default-subjekt falsch: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"public":true}`)); got != "helpdesk:comment_public" {
		t.Fatalf("subject_when greift nicht: %s", got)
	}
	if got := sys.ActionSubject("comment", []byte(`{"public":false}`)); got != "helpdesk:comment" {
		t.Fatalf("subject_when zu breit: %s", got)
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

	sys := NewManifestSystem(mustManifest(t))
	cred := Credential{BaseURL: srv.URL, Token: "tok-123"}

	if _, err := sys.Execute(context.Background(), "get_issue", []byte(`{"issue_id":7}`), cred); err != nil {
		t.Fatal(err)
	}
	if gotPath != "GET /issues/7" || gotAuth != "tok-123" {
		t.Fatalf("request falsch: %s auth=%s", gotPath, gotAuth)
	}

	if _, err := sys.Execute(context.Background(), "comment",
		[]byte(`{"issue_id":7,"text":"Hallo","public":true}`), cred); err != nil {
		t.Fatal(err)
	}
	if gotPath != "POST /issues/7/comments" {
		t.Fatalf("pfad falsch: %s", gotPath)
	}
	// issue_id ist im Pfad verbraucht — nur die restlichen Params gehen als Body.
	if strings.Contains(gotBody, "issue_id") || !strings.Contains(gotBody, "Hallo") {
		t.Fatalf("body falsch: %s", gotBody)
	}

	if _, err := sys.Execute(context.Background(), "get_issue", []byte(`{}`), cred); err == nil {
		t.Fatal("unaufgelöster platzhalter muss ein fehler sein")
	}
	if _, err := sys.Execute(context.Background(), "kaboom", []byte(`{}`), cred); err == nil {
		t.Fatal("unbekannte aktion muss ein fehler sein")
	}
}
