package integration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// Where a runner fetches its next binary from is the operator's to say, not the
// request's: the checksums lie beside the archive, so whoever names the origin
// names the program the host runs next. The update route takes a version and
// nothing else; the order carries the operator's COVEY_RUNNER_DOWNLOAD_BASE.
func TestTheUpdateRouteDoesNotTakeADownloadOriginFromTheRequest(t *testing.T) {
	dir := t.TempDir()
	s, pool, _ := remoteStack(t, dir)

	var operatorHits, requestHits atomic.Int32
	operator := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		operatorHits.Add(1)
		http.NotFound(w, r)
	}))
	defer operator.Close()
	requested := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestHits.Add(1)
		http.NotFound(w, r)
	}))
	defer requested.Close()
	// The operator's base answers 404, so the update fails after the host
	// asked it for SHA256SUMS and nothing is replaced.
	s.srv.RunnerDownloadBase = operator.URL

	runnerID := connectRemoteRunner(t, s, pool, dir, nil)
	c := login(t, s, "admin@test.local", "admin-passwort")
	path := "/api/v1/runners/" + runnerID.String() + "/update"

	res := c.expect(http.MethodPost, path, map[string]any{"version": "v9.9.9", "base_url": requested.URL}, http.StatusOK)
	if res["ok"] != false {
		t.Fatalf("the update has to be refused: %v", res)
	}
	msg, _ := res["error"].(string)
	if strings.Contains(msg, requested.URL) || !strings.Contains(msg, operator.URL) {
		t.Errorf("the order has to carry the operator's base, not the request's: %q", msg)
	}
	if requestHits.Load() != 0 {
		t.Errorf("the origin from the request was fetched from %d times", requestHits.Load())
	}
	if operatorHits.Load() == 0 {
		t.Error("the operator's base was never asked")
	}

	// The version ends up in a URL and a file name on the host.
	for _, v := range []string{"../../x", "v1.2.3/../evil", "latest"} {
		c.expect(http.MethodPost, path, map[string]any{"version": v}, http.StatusBadRequest)
	}
}
