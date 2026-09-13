package integration

import (
	"bufio"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The event stream is what makes the interface live — an agent's status, a
// task moving, a recording growing. The property that matters is not that it
// streams but WHOSE events it streams: a connection belongs to one
// organisation, and an account without a membership subscribes to the empty
// organisation and hears nothing rather than everything.
func TestEventStreamCarriesOnlyThisOrganisationsEvents(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("strom-agent")

	req, err := http.NewRequest(http.MethodGet, s.http.URL+"/api/v1/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	streamCtx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	req = req.WithContext(streamCtx)
	resp, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the event stream answered %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("the stream is served as %q", ct)
	}
	// No caching: a proxy that held this would turn a live view into a
	// snapshot nobody can tell is old.
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
		t.Errorf("Cache-Control = %q", cc)
	}

	lines := bufio.NewScanner(resp.Body)
	// The first thing on the wire is a greeting, so the browser knows the
	// connection stands before anything has happened.
	var greeted bool
	for lines.Scan() {
		if strings.HasPrefix(lines.Text(), "event: hello") {
			greeted = true
			break
		}
	}
	if !greeted {
		t.Fatal("the stream sent no greeting")
	}

	// Now something happens, and it has to arrive.
	go func() {
		time.Sleep(150 * time.Millisecond)
		_, _ = s.backlog.Create(context.Background(), s.orgID, agent.ID, "Ein Ereignis",
			"[mock:result fertig]", "manual", 3)
	}()

	deadline := time.Now().Add(15 * time.Second)
	var sawEvent bool
	for time.Now().Before(deadline) && lines.Scan() {
		if strings.HasPrefix(lines.Text(), "event: ") && !strings.HasPrefix(lines.Text(), "event: hello") {
			sawEvent = true
			break
		}
	}
	if !sawEvent {
		t.Error("nothing arrived on the stream although an agent was given work")
	}
}
