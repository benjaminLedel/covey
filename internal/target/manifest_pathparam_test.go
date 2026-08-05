package target

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Der Pfad einer Manifest-Aktion ist die Grenze, auf die die Aktion gescopt
// ist — die Guard-Rail regelt `system:action`, und der Manifest-Autor hat
// entschieden, welchen Endpunkt das erreicht. Ein Parameter, der unescaped
// eingesetzt wird, bricht aus dieser Grenze aus.
//
// Die Werte kommen vom Agenten, und nach dem eigenen Bedrohungsmodell
// (spec/04) ist der keine vertrauenswürdige Quelle: ein prompt-injizierter
// Agent ist genau der Fall, für den das hier steht.
func TestManifestPfadParameterBrichtNichtAus(t *testing.T) {
	var gotRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RequestURI, nicht URL.Path: der Server normalisiert Punkt-Segmente
		// sonst weg, und dann prüfte der Test die Normalisierung statt den Fix.
		gotRaw = r.RequestURI
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	sys := NewManifestSystem(mustManifest(t))
	cred := Credential{BaseURL: srv.URL, Token: "tok-123"}

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
		// url.PathEscape allein hilft hier nicht: Punkte werden nicht kodiert,
		// und ein Segment, das nur aus ".." besteht, verschiebt die Anfrage
		// auch ohne eigenen Schrägstrich eine Ebene nach oben.
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

	// Der Normalfall muss unberührt bleiben — ein Fix, der gewöhnliche Werte
	// kaputt macht, wird wieder ausgebaut.
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
