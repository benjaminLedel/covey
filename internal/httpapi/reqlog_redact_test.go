package httpapi

import (
	"net/url"
	"testing"

	"covey/internal/reqlog"
)

// TestTriggerTokensStayOutOfTheRequestLog: the trigger routes carry their
// bearer secret as the last path segment; the logged URL must not.
func TestTriggerTokensStayOutOfTheRequestLog(t *testing.T) {
	for in, want := range map[string]string{
		"/api/trigger/abc123":                "/api/trigger/***",
		"/api/trigger/workflows/abc123":      "/api/trigger/workflows/***",
		"/api/trigger/workflows/abc%2F123":   "/api/trigger/workflows/***",
		"/api/trigger/abc123?x=1&token=s":    "/api/trigger/***?token=%2A%2A%2A&x=1",
		"/api/webhooks/zammad/workflows/id1": "/api/webhooks/zammad/workflows/id1",
	} {
		u, err := url.Parse(in)
		if err != nil {
			t.Fatal(err)
		}
		if got := reqlog.RedactURL(redactTokenPath(u)); got != want {
			t.Errorf("%s: logged as %q, want %q", in, got, want)
		}
	}
}
