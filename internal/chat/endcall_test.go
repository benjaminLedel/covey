package chat

import (
	"context"
	"strings"
	"testing"
)

// TestTriageSchluss: the closing of a call (#517). The model's end_call
// stands for a goodbye said in a call, and nowhere else: not for a typed
// message, not for an answer to a message that asks something, not for a
// search or a config proposal.
func TestTriageSchluss(t *testing.T) {
	faelle := []struct {
		name      string
		anruf     bool
		nachricht string
		antwort   string
		will      bool
	}{
		{"goodbye in a call", true, "Super, danke dir, das war's. Tschüss!",
			`{"action":"answer","text":"Gern, bis später!","spoken":"Gern, bis später!","end_call":true}`, true},
		{"that's all, bye", true, "Great, thanks, that's all. Bye!",
			`{"action":"answer","text":"Anytime, bye!","spoken":"Anytime, bye!","end_call":true}`, true},
		{"a job handed over while leaving", true, "Schick Bernd bitte noch die Zusammenfassung. Danke, tschüss!",
			`{"action":"task","title":"Bernd die Zusammenfassung schicken","body":"…","text":"Mach ich, ich meld mich im Chat. Tschüss!","spoken":"Mach ich, ich meld mich im Chat. Tschüss!","end_call":true}`, true},
		{"a thank-you with a question after it", true, "Danke! Und ist die Gutschrift schon verbucht?",
			`{"action":"answer","text":"Ja, seit heute Morgen.","spoken":"Ja, seit heute Morgen.","end_call":true}`, false},
		{"a thank-you mid-request, as the model should mark it", true, "Danke, und kannst du noch den Bericht an Bernd schicken",
			`{"action":"task","title":"Bericht an Bernd schicken","body":"…","text":"Mach ich.","spoken":"Mach ich."}`, false},
		{"a typed goodbye", false, "Danke, tschüss!",
			`{"action":"answer","text":"Gern, bis später!","spoken":"Gern.","end_call":true}`, false},
		{"a search ends nothing", true, "Danke, tschüss!",
			`{"action":"search","query":"Globex","end_call":true}`, false},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			m := &fangModell{antwort: f.antwort}
			e, err := Triagieren(context.Background(), m, Rahmen{Rolle: "Demo", Anruf: f.anruf}, "", nil, nil, nil, f.nachricht, nil)
			if err != nil {
				t.Fatal(err)
			}
			if e.Schluss != f.will {
				t.Fatalf("end_call = %v, want %v: %+v", e.Schluss, f.will, e)
			}
			meta := e.AnswerMeta()
			if got := meta[MetaEndCall] == "true"; got != f.will {
				t.Fatalf("meta %v, want end_call %v", meta, f.will)
			}
			if !f.will && meta[MetaEndCall] != "" {
				t.Fatalf("meta carries end_call: %v", meta)
			}
			if f.anruf != strings.Contains(m.system, `"end_call"`) {
				t.Fatalf("the closing instruction in the system prompt: %v, want %v", !f.anruf, f.anruf)
			}
		})
	}
}

// TestEndetAnruf: which decisions may close a call.
func TestEndetAnruf(t *testing.T) {
	for _, f := range []struct {
		aktion    Aktion
		nachricht string
		will      bool
	}{
		{AktionAntwort, "Danke, das war's.", true},
		{AktionAntwort, "Danke. Wie weit ist Globex?", false},
		{AktionAntwort, "Thanks — anything else from Bernd？", false},
		{AktionNotiz, "Zur Rechnung noch: Gutschrift kam. Tschüss!", true},
		{AktionAufgabe, "Kannst du Bernd den Bericht schicken? Danke, tschüss!", true},
		{AktionKonfig, "Schau nur noch werktags rein. Tschüss!", false},
		{AktionSuche, "Tschüss!", false},
	} {
		if got := endetAnruf(Entscheidung{Aktion: f.aktion}, f.nachricht); got != f.will {
			t.Errorf("%s after %q: %v, want %v", f.aktion, f.nachricht, got, f.will)
		}
	}
}

// TestWithEndCallKopiert: marking the goodbye copies the meta, as the
// spoken form does, and leaves it alone when there is nothing to mark.
func TestWithEndCallKopiert(t *testing.T) {
	stimme := map[string]string{"voice_reason": "none×chat"}
	out := WithEndCall(stimme, true)
	if out[MetaEndCall] != "true" || out["voice_reason"] != "none×chat" || len(stimme) != 1 {
		t.Fatalf("marked: %v, original %v", out, stimme)
	}
	if got := WithEndCall(stimme, false); len(got) != 1 {
		t.Fatalf("unmarked: %v", got)
	}
}

// TestSplitSpokenCall: a chat answer's run marks its goodbye in the tag
// (#517), beside details_in_chat and in either order; a tag without a spoken
// form ends nothing.
func TestSplitSpokenCall(t *testing.T) {
	w, g, d, e := SplitSpokenCall("Gern, bis später!\n<spoken end_call=\"true\">Gern, bis später!</spoken>")
	if w != "Gern, bis später!" || g != "Gern, bis später!" || d || !e {
		t.Fatalf("split: %q | %q | %v | %v", w, g, d, e)
	}
	_, _, d, e = SplitSpokenCall("Done.\n<spoken end_call='true' details_in_chat=\"true\">Done, bye.</spoken>")
	if !d || !e {
		t.Fatalf("both attributes: details %v, end %v", d, e)
	}
	_, _, d, e = SplitSpokenCall(geschriebenMR + "\n<spoken details_in_chat=\"true\">" + gesprochenMR + "</spoken>")
	if !d || e {
		t.Fatalf("details only: details %v, end %v", d, e)
	}
	if _, g, _, e := SplitSpokenCall("Bye.\n<spoken end_call=\"true\">  </spoken>"); g != "" || e {
		t.Fatalf("an empty spoken form: %q, end %v", g, e)
	}
	// SplitSpoken reads the same tag and ignores the closing.
	if w, g, _ := SplitSpoken("Bye!\n<spoken end_call=\"true\">Bye!</spoken>"); w != "Bye!" || g != "Bye!" {
		t.Fatalf("SplitSpoken with end_call: %q | %q", w, g)
	}
}
