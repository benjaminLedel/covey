package orchestrator

import (
	"context"
	"testing"
	"time"
)

/* Abbrechen ist ein Signal, kein Abwarten. Wer danach aufräumt — ein Test sein
   Verzeichnis, ein Deploy seine Container —, räumt sonst unter noch laufender
   Arbeit weg. Gemessen: „TempDir RemoveAll cleanup: directory not empty" bei
   einem Test, dessen eigene Prüfungen alle gehalten hatten. */

// Run kehrt erst zurück, wenn die eigene Nebenläufigkeit aufgehört hat.
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

// Und es wartet nicht ewig: eine Sitzung, die ihren Abbruch nicht bemerkt,
// darf nicht das Herunterfahren der ganzen Plattform aufhalten. Die Frist ist
// die Grenze zwischen „sauber beenden" und „hängen".
func TestDasHerunterfahrenHatEineFrist(t *testing.T) {
	if shutdownGrace > time.Minute {
		t.Fatalf("die Frist ist %s — so lange hält kein Deploy still", shutdownGrace)
	}
	if shutdownGrace < 5*time.Second {
		t.Fatalf("die Frist ist %s — das reicht nicht für einen Abbau, der noch einen Container stoppt", shutdownGrace)
	}
}

// Der Helfer ist der einzige Weg hinein: was daran vorbei gestartet wird,
// holt niemand mehr ein. Hier wird nur festgehalten, dass er zählt.
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
