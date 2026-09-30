package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDictationCleanShortTurn: a turn said in a call under four words comes
// back as recognised, without a model (#511) — "Hallo" had come back as
// "Ja.".
func TestDictationCleanShortTurn(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/dictation/clean", strings.NewReader(`{"text":" Hallo ","app":"covey call","turn":true}`))
	(&Server{}).handleDictationClean(rec, req)
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if out["text"] != "Hallo" || out["kept"] != "short" {
		t.Fatalf("%v", out)
	}
}
