package main

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWithAuth: the ONE gate between an agent action and running Xcode/simctl
// on this host is this bearer-token check — it must reject a wrong or
// missing token and only let through the exact configured one.
func TestWithAuth(t *testing.T) {
	b := &bridge{cfg: config{Token: "correct-token"}, log: slog.Default(), builds: map[string]*buildRecord{}}
	called := false
	h := b.withAuth(func(w http.ResponseWriter, r *http.Request) { called = true })

	cases := []struct {
		name   string
		header string
		want   int
		calls  bool
	}{
		{"missing", "", http.StatusUnauthorized, false},
		{"wrong", "Bearer nope", http.StatusUnauthorized, false},
		{"correct", "Bearer correct-token", http.StatusOK, true},
	}
	for _, c := range cases {
		called = false
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/build/x/log", nil)
		if c.header != "" {
			req.Header.Set("Authorization", c.header)
		}
		h(rec, req)
		if rec.Code != c.want {
			t.Errorf("%s: status = %d, want %d", c.name, rec.Code, c.want)
		}
		if called != c.calls {
			t.Errorf("%s: handler called = %v, want %v", c.name, called, c.calls)
		}
	}
}

func TestHandleHealthNoAuthRequired(t *testing.T) {
	b := &bridge{cfg: config{Token: "t"}, log: slog.Default(), builds: map[string]*buildRecord{}}
	rec := httptest.NewRecorder()
	b.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status = %d, want 200", rec.Code)
	}
}
