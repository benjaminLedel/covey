package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/llm"
)

func TestLesen(t *testing.T) {
	faelle := []struct {
		name string
		roh  string
		will Aktion
		fehl bool
	}{
		{"nacktes JSON", `{"action":"answer","text":"Ja, heute Morgen."}`, AktionAntwort, false},
		{"im Codeblock", "```json\n{\"action\":\"task\",\"title\":\"Rechnung prüfen\",\"body\":\"…\"}\n```", AktionAufgabe, false},
		{"mit Vorrede", `Sure! {"action":"answer","text":"👀"}`, AktionAntwort, false},
		{"Antwort ohne Text", `{"action":"answer","text":"  "}`, "", true},
		{"Aufgabe ohne Titel", `{"action":"task","body":"x"}`, "", true},
		{"unbekannte Aktion", `{"action":"delegate"}`, "", true},
		// #457: a short chat line without any JSON is what the model said to
		// the people — an answer, not a failed triage.
		{"nacktes Emoji", "😀", AktionAntwort, false},
		{"kurzer Satz ohne JSON", "Hey Ada, alles gut hier — ich sitz an der Globex-Rechnung.", AktionAntwort, false},
		{"leer", "   ", "", true},
		{"kaputtes JSON", `{"action":"answer","text":"Hallo`, "", true},
		{"halbe Klammer", `"action":"task","title":"x"}`, "", true},
		{"Codeblock ohne JSON", "```\nhallo\n```", "", true},
		{"zu lang für einen Zuruf", strings.Repeat("Das ist ein langer Satz. ", 20), "", true},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			e, err := lesen(f.roh)
			if f.fehl {
				if err == nil {
					t.Fatalf("expected an error, got %+v", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if e.Aktion != f.will {
				t.Fatalf("action: %q", e.Aktion)
			}
		})
	}
}

// TestLesenKennt die drei Aktionen — und lehnt die Notiz ohne Ziel ab. Eine
// Notiz ohne Aufgabe wäre ein Text, der nirgends landet, und genau das darf
// bei einer Nachricht nie passieren.
func TestLesenNotiz(t *testing.T) {
	e, err := lesen(`{"action":"note","task":"ab12","text":"Der Kunde hat nachgereicht."}`)
	if err != nil {
		t.Fatal(err)
	}
	if e.Aktion != AktionNotiz || e.Aufgabe != "ab12" {
		t.Fatalf("note: %+v", e)
	}
	if _, err := lesen(`{"action":"note","text":"ohne Ziel"}`); err == nil {
		t.Fatal("a note without a task must not pass")
	}
	if _, err := lesen(`{"action":"note","task":"ab12"}`); err == nil {
		t.Fatal("a note without text must not pass")
	}
}

// TestLesenDirektBehaeltDenText: the reply is posted as it came, trimmed.
func TestLesenDirektBehaeltDenText(t *testing.T) {
	e, err := lesen("  👋 \n")
	if err != nil {
		t.Fatal(err)
	}
	if e.Aktion != AktionAntwort || e.Text != "👋" {
		t.Fatalf("direct reply: %+v", e)
	}
}

// TestRaumBeschreibtDieGruppe: in a group the agent is told who else reads
// along and that it is one colleague among them (#440, #457); the sentence
// about being addressed belongs to the triage only. A direct conversation
// gets nothing — the person is described already.
func TestRaumBeschreibtDieGruppe(t *testing.T) {
	ich, kollege := uuid.New(), uuid.New()
	gruppe := Conversation{Kind: KindGroup, Title: "Rechnungen", Members: []Member{
		{Kind: MemberAgent, ID: ich, Name: "Demo"},
		{Kind: MemberAgent, ID: kollege, Name: "Quirina"},
		{Kind: MemberHuman, ID: uuid.New(), Name: "Ada Lovelace"},
	}}
	r := Raum(gruppe, ich, "Ada Lovelace", true)
	for _, will := range []string{`"Rechnungen"`, "Quirina (AI colleague)", "Ada Lovelace", "one colleague among them", "Address Ada by name", "addresses you"} {
		if !strings.Contains(r, will) {
			t.Errorf("group text lacks %q: %s", will, r)
		}
	}
	if strings.Contains(r, "Demo") {
		t.Errorf("the agent itself is not a member it talks to: %s", r)
	}
	if strings.Contains(Raum(gruppe, ich, "Ada Lovelace", false), "addresses you") {
		t.Error("the narration was not addressed by a new message")
	}
	if !strings.Contains(Raum(gruppe, ich, "", true), "first name") {
		t.Error("without a known author the agent is still told to use the first name")
	}
	if Raum(Conversation{Kind: KindDirect, Members: gruppe.Members}, ich, "Ada Lovelace", true) != "" {
		t.Error("a direct conversation has no group text")
	}
}

// fangModell answers with a fixed text and keeps what it was shown.
type fangModell struct {
	antwort        string
	system, prompt string
}

func (m *fangModell) Name() string { return "fang" }
func (m *fangModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.system, m.prompt = req.System, req.Messages[0].Content
	return m.antwort, nil
}

// TestErzaehlenSiehtDieGruppe: the narration gets the frame the triage gets —
// in a group, who reads along.
func TestErzaehlenSiehtDieGruppe(t *testing.T) {
	m := &fangModell{antwort: "Ada, die Rechnung war doppelt gebucht."}
	r := Rahmen{Rolle: "Demo — Support", Gegenueber: "Ada Lovelace", Raum: "This is the group conversation \"Rechnungen\"."}
	text, err := Erzaehlen(context.Background(), m, r, nil, "Rechnung prüfen", "done", "Doppelt gebucht, storniert.")
	if err != nil || text == "" {
		t.Fatal(text, err)
	}
	for _, will := range []string{"Demo — Support", "Ada Lovelace", "group conversation", "Doppelt gebucht"} {
		if !strings.Contains(m.prompt, will) {
			t.Errorf("narration prompt lacks %q", will)
		}
	}
	if !strings.Contains(m.system, "Never talk about the machinery") {
		t.Error("the narration must be told not to talk about tasks and runs")
	}
}
