package chat

import "testing"

// A call is closed only on the person's farewell (#521).
func TestSaysGoodbye(t *testing.T) {
	yes := []string{"Tschüss!", "Okay, danke, bis später.", "Das war's, ciao", "Schönen Abend noch", "Mach’s gut", "Thanks, bye!", "That's all, see you", "Good bye"}
	no := []string{"Tandu.", "Hallo?", "Danke, und kannst du noch die Rechnung prüfen?", "Ja.", "Okay.", "Byers ist der Kunde", "Ich sehe das Ende nicht", ""}
	for _, s := range yes {
		if !SaysGoodbye(s) {
			t.Errorf("%q should be a farewell", s)
		}
	}
	for _, s := range no {
		if SaysGoodbye(s) {
			t.Errorf("%q should not be a farewell", s)
		}
	}
}

// A misheard short turn the model reads as a closing doesn't close the call.
func TestAMisheardWordDoesNotCloseTheCall(t *testing.T) {
	e := Entscheidung{Aktion: AktionAntwort, Text: "Bis später!", Gesprochen: "Bis später!", Schluss: true}
	if got := fuerAnruf(e, true, "de", "Tandu."); got.Schluss {
		t.Fatal("a misheard word closed the call")
	}
	if got := fuerAnruf(e, true, "de", "Okay, tschüss!"); !got.Schluss {
		t.Fatal("a farewell no longer closes the call")
	}
}
