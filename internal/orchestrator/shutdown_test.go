package orchestrator

import (
	"context"
	"testing"
	"time"
)

/* Cancellation is a signal, not a wait. Whoever cleans up after it — a
   test its directory, a deploy its containers — otherwise cleans away work
   that is still running. Measured in a test whose own checks had all
   held: `TempDir RemoveAll cleanup: directory not empty`. */

// Run returns only once its own concurrency has stopped.
func TestRunWartetAufSeineNebenlaeufigkeit(t *testing.T) {
	o := &Orchestrator{}
	losgelassen := make(chan struct{})
	angekommen := make(chan struct{})
	o.nebenlaeufig(func() {
		close(angekommen)
		<-losgelassen
	})
	<-angekommen

	fertig := make(chan struct{})
	go func() {
		o.shutdown()
		close(fertig)
	}()

	select {
	case <-fertig:
		t.Fatal("das Herunterfahren war durch, während noch etwas lief")
	case <-time.After(200 * time.Millisecond):
	}

	close(losgelassen)
	select {
	case <-fertig:
	case <-time.After(5 * time.Second):
		t.Fatal("das Herunterfahren kam nicht zum Ende, obwohl nichts mehr läuft")
	}
}

// And it does not wait forever: a session that does not notice its
// cancellation must not hold up the shutdown of the whole platform. The
// deadline is the line between ending cleanly and hanging.
func TestDasHerunterfahrenHatEineFrist(t *testing.T) {
	if shutdownGrace > time.Minute {
		t.Fatalf("die Frist ist %s — so lange hält kein Deploy still", shutdownGrace)
	}
	if shutdownGrace < 5*time.Second {
		t.Fatalf("die Frist ist %s — das reicht nicht für einen Abbau, der noch einen Container stoppt", shutdownGrace)
	}
}

// The helper is the only way in: whatever starts past it, nobody collects
// it any more. This only records that it counts.
func TestJedeNebenlaeufigkeitWirdGezaehlt(t *testing.T) {
	o := &Orchestrator{}
	ctx, abbrechen := context.WithCancel(context.Background())
	for i := 0; i < 5; i++ {
		o.nebenlaeufig(func() { <-ctx.Done() })
	}
	fertig := make(chan struct{})
	go func() {
		o.laufend.Wait()
		close(fertig)
	}()
	select {
	case <-fertig:
		t.Fatal("gewartet wurde auf nichts")
	case <-time.After(100 * time.Millisecond):
	}
	abbrechen()
	select {
	case <-fertig:
	case <-time.After(5 * time.Second):
		t.Fatal("die fünf wurden nicht eingeholt")
	}
}
