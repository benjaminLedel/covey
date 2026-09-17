package manifestplug

import (
	"context"
	"encoding/json"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The path of a manifest action is the boundary the action is scoped to — the
// guard-rail governs `system:action`, and the manifest author decided which
// endpoint that reaches. A parameter inserted unescaped breaks out of this
// boundary.
//
// The values come from the agent, and by our own threat model
// (spec/04) that is no trusted source: a prompt-injected agent is exactly the
// case this stands for.
func TestManifestPfadParameterBrichtNichtAus(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RequestURI, not URL.Path: the server otherwise normalises dot segments
		// away, and then the test would check the normalisation instead of the fix.
		gotRaw = r.RequestURI
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	sys := New(mustManifest(t))
	cred := target.Credential{BaseURL: srv.URL, Token: "tok-123"}

	t.Run("Traversal landet nicht im Pfad", func(t *testing.T) {
		gotRaw = ""
		_, err := sys.Execute(context.Background(), "get_issue",
			[]byte(`{"issue_id":"../../admin/users"}`), cred)
		if err != nil {
			t.Fatalf("die Aktion soll durchlaufen, der Wert nur entschärft sein: %v", err)
		}
		if strings.Contains(gotRaw, "../") {
			t.Fatalf("RequestURI=%q — der Wert ist unescaped durchgereicht worden", gotRaw)
		}
		if !strings.HasPrefix(gotRaw, "/issues/") {
			t.Fatalf("RequestURI=%q — die Aktion hat ihren Endpunkt verlassen", gotRaw)
		}
	})

	t.Run("ein reines Punkt-Segment wird abgelehnt", func(t *testing.T) {
		// url.PathEscape alone does not help here: dots are not encoded,
		// and a segment made only of ".." moves the request
		// one level up even without a slash of its own.
		for _, wert := range []string{`".."`, `"."`} {
			if _, err := sys.Execute(context.Background(), "get_issue",
				[]byte(`{"issue_id":`+wert+`}`), cred); err == nil {
				t.Errorf("%s wurde akzeptiert", wert)
			}
		}
	})

	t.Run("Steuerzeichen werden abgelehnt", func(t *testing.T) {
		if _, err := sys.Execute(context.Background(), "get_issue",
			[]byte(`{"issue_id":"7\r\nX-Injected: 1"}`), cred); err == nil {
			t.Error("CR/LF im Pfadparameter wurde akzeptiert")
		}
	})

	// The normal case must stay untouched — a fix that breaks ordinary values
	// gets taken back out.
	t.Run("gewöhnliche Werte gehen unverändert durch", func(t *testing.T) {
		gotRaw = ""
		if _, err := sys.Execute(context.Background(), "get_issue",
			[]byte(`{"issue_id":7}`), cred); err != nil {
			t.Fatal(err)
		}
		if gotRaw != "/issues/7" {
			t.Fatalf("RequestURI=%q, erwartet /issues/7", gotRaw)
		}
	})
}

func TestCheckPathParam(t *testing.T) {
	ok := []string{"7", "abc", "a.b", "...", "hallo welt", "ä", "a-b_c"}
	nichtOk := []string{".", "..", "a\nb", "a\rb", "a\x00b", "a\x7fb"}

	for _, v := range ok {
		if err := checkPathParam("p", v); err != nil {
			t.Errorf("%q abgelehnt: %v", v, err)
		}
	}
	for _, v := range nichtOk {
		if err := checkPathParam("p", v); err == nil {
			t.Errorf("%q akzeptiert", v)
		}
	}
}
