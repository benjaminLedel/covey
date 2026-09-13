package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"covey/internal/style"
)

// The corpus for the test: four short texts with the shapes a build has to tell
// apart. Short on purpose — what is being checked is the path from an upload to
// the TONE.md an agent carries, not the quality of the bands.
var voiceCorpus = [][2]string{
	{"post-1.md", `Am Montag stand die Auslieferung still, und niemand hatte eine Meldung
bekommen. Der Build lief durch, der Deploy meldete Erfolg, und trotzdem
antwortete die Instanz mit der Version von Freitag.

Die Zahlen dazu sind unspektakulär und trotzdem der Kern: 206.000 von 262.000
Ereignissen und 376 von 384 MB waren der wörtliche Verlauf der Läufe. Der Rest,
also alles, was jemand später wirklich liest, macht acht Megabyte aus.

Ein Beispiel dafür ist der Agent, der seit sechs Wochen ohne Neustart lief. Er
arbeitete mit dem Code von vorgestern, weil seine Sandbox das Image behielt, mit
dem sie gestartet war.`},
	{"post-2.md", `Die zweite Woche fing an wie die erste, mit einer Meldung um 6:40 Uhr und
einem Ticket, das niemand zuordnen konnte. Diesmal lag es an der Uhr: Der Runner
stand auf UTC, die Steuerebene auf Europe/Berlin.

Wir haben danach drei Dinge geändert, und nur eines davon war Code. Die Zeitzone
steht jetzt in der Konfiguration des Runners, der Heartbeat protokolliert seine
Basiszeit, und die Dokumentation nennt beide Stellen nebeneinander.`},
	{"post-3.md", `Eine Frage, die mir seit Jahren gestellt wird: Wie viele Agenten braucht ein
Team? Die ehrliche Antwort ist, dass die Zahl nicht das Problem ist.

Im September haben wir es gemessen. Von 74 Aufgaben liefen 61 durch eine einzige
Hand, 11 über zwei und 2 über drei. Die beiden Dreierketten haben zusammen so
lange gebraucht wie die 61 anderen.`},
	{"post-4.md", `Der Mittwoch gehörte dem Speicher. Ein Home von 7,3 GB wurde bei jedem Wecken
gescannt, und der Scan dauerte elf Minuten, bevor überhaupt etwas passierte.

Die Lösung war unspektakulär: Blöcke statt Dateien, und ein Marker, der sagt, ob
die Kopie jünger ist als der Snapshot. Danach lag dieselbe Operation bei achtzehn
Sekunden.`},
}

// The whole way a voice travels: texts in, a build, a card released by a
// person, and an agent that carries the result as its TONE.md — which is what
// the style gate measures against and what the model reads while writing
// (spec/24).
func TestVoiceReachesAnAgentAsItsToneFile(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("writer")

	created := admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "Hausstimme", "language": "de"}, http.StatusCreated)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("no id: %+v", created)
	}

	// A name is taken once. Two voices of the same name would make the picker
	// in the agent settings a guess.
	admin.expect(http.MethodPost, "/api/v1/voices",
		map[string]any{"name": "hausstimme"}, http.StatusConflict)

	// Nothing to build from yet, and the refusal says so rather than producing
	// empty bands that would then measure every text as wrong.
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/build", nil, http.StatusBadRequest)

	for _, doc := range voiceCorpus {
		admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/documents",
			map[string]any{"name": doc[0], "body": doc[1]}, http.StatusCreated)
	}
	// A list is not a corpus: measured as prose it has no anchors and drags
	// every band with it, so it is refused where it is pasted in.
	admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/documents",
		map[string]any{"name": "liste.md", "body": "- eins\n- zwei\n- drei\n"}, http.StatusBadRequest)

	built := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/build", nil, http.StatusOK)
	if v, _ := built["version"].(float64); v != 1 {
		t.Fatalf("the build has to count: %+v", built["version"])
	}
	exemplars, _ := built["exemplars"].([]any)
	if len(exemplars) < 3 {
		t.Fatalf("too few exemplars: %d", len(exemplars))
	}
	// No control-plane credential in the test stack, so no card — and the build
	// says so instead of failing. Three of the four artefacts are measured and
	// stand without a model.
	notes, _ := built["notes"].([]any)
	if !containsNote(notes, "credential") {
		t.Errorf("the missing card has to be reported: %+v", notes)
	}

	// An unbuilt voice cannot be assigned; a built one without a released card
	// can — the card is the only part that waits for a person.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": id}, http.StatusOK)

	cfg, err := s.registry.CurrentConfig(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	tone := cfg.Files["TONE.md"]
	if tone == "" {
		t.Fatal("assigning a voice has to write the TONE.md — that is what acts")
	}
	if !strings.Contains(tone, "Hausstimme") {
		t.Errorf("the TONE.md does not name the voice:\n%s", tone)
	}
	// The half the gate reads has to survive the rendering, otherwise the guard
	// behind the voice is missing and nothing says so.
	profile, prose, err := style.ParseProfile(tone)
	if err != nil {
		t.Fatalf("the gate cannot read the profile back: %v", err)
	}
	if len(profile.Bands) == 0 {
		t.Error("a TONE.md without bands measures nothing")
	}
	if !strings.Contains(prose, "Passagen") && !strings.Contains(prose, "Am Montag") {
		t.Errorf("the passages belong in the prose the model reads:\n%s", prose)
	}
	// And the agent says whose voice it carries.
	after, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.VoiceID == nil || after.VoiceID.String() != id {
		t.Fatalf("the agent does not carry the voice: %+v", after.VoiceID)
	}

	// Releasing a card: what a person releases is what they release, corrections
	// included.
	released := admin.expect(http.MethodPost, "/api/v1/voices/"+id+"/release",
		map[string]any{"card": "Der Autor öffnet mit einer Beobachtung."}, http.StatusOK)
	if released["released_at"] == nil {
		t.Fatalf("the release has to be recorded: %+v", released)
	}
	// It reaches the agent at the next assignment, not behind its back: the
	// TONE.md is a config version, and a version does not change by itself.
	if cfg2, _ := s.registry.CurrentConfig(ctx, agent.ID); strings.Contains(cfg2.Files["TONE.md"], "Beobachtung") {
		t.Error("a release must not rewrite a config version that already stands")
	}
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": id}, http.StatusOK)
	cfg3, _ := s.registry.CurrentConfig(ctx, agent.ID)
	if !strings.Contains(cfg3.Files["TONE.md"], "Der Autor öffnet mit einer Beobachtung.") {
		t.Errorf("the released card has to reach the agent on re-assignment:\n%s", cfg3.Files["TONE.md"])
	}

	// Taking the voice off leaves the file: removing it would change how the
	// agent writes as a side effect of a picker.
	admin.expect(http.MethodPut, "/api/v1/agents/"+agent.ID.String()+"/voice",
		map[string]any{"voice_id": ""}, http.StatusOK)
	cleared, _ := s.registry.Get(ctx, agent.ID)
	if cleared.VoiceID != nil {
		t.Error("the link has to go")
	}
	if cfg4, _ := s.registry.CurrentConfig(ctx, agent.ID); cfg4.Files["TONE.md"] == "" {
		t.Error("the TONE.md stays — it is a config version like any other")
	}
}

func containsNote(notes []any, substr string) bool {
	for _, n := range notes {
		if s, ok := n.(string); ok && strings.Contains(s, substr) {
			return true
		}
	}
	return false
}
