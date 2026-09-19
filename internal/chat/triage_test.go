package chat

import "testing"

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
		{"kein JSON", `I think this is a task.`, "", true},
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
