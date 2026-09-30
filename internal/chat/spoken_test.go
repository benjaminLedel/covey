package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"covey/internal/llm"
)

const (
	geschriebenMR = "Doch, ist drin – DLES-273, MR !475, alle Tests grün, wartet auf Gertruds Review."
	gesprochenMR  = "Ja, ist drin. Der Merge Request ist fertig, alle Tests sind grün, und er wartet noch auf Gertruds Review."
)

// TestTriageImAnruf: a message said in a call gets the spoken form asked for
// and kept (#502); the written text is what it was.
func TestTriageImAnruf(t *testing.T) {
	m := &fangModell{antwort: `{"action":"answer","text":"` + geschriebenMR + `","spoken":"` + gesprochenMR + `","details_in_chat":true}`}
	e, err := Triagieren(context.Background(), m, Rahmen{Rolle: "Dev", Anruf: true}, "", nil, nil, nil, "Der Login-Fix ist noch nicht drin, oder?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Text != geschriebenMR || e.Gesprochen != gesprochenMR || !e.DetailsImChat {
		t.Fatalf("decision: %+v", e)
	}
	if !strings.Contains(m.system, `"spoken"`) || !strings.Contains(m.system, "details_in_chat") {
		t.Fatalf("the call's instruction is missing from the system prompt:\n%s", m.system)
	}
	if !strings.Contains(m.prompt, "said aloud in a call") {
		t.Fatalf("the prompt does not say the message was said in a call:\n%s", m.prompt)
	}
	meta := (Entscheidung{Aktion: AktionAntwort, Text: e.Text, Gesprochen: e.Gesprochen, DetailsImChat: true,
		Meta: map[string]string{"voice_reason": "none×chat"}}).AnswerMeta()
	if meta[MetaSpoken] != gesprochenMR || meta[MetaDetailsInChat] != "true" || meta["voice_reason"] != "none×chat" {
		t.Fatalf("meta: %v", meta)
	}
}

// TestTriageGeschriebenUnveraendert: a typed message is triaged with the
// prompt it had before #502, and a spoken form the model offers anyway is
// dropped — a written message never gets one.
func TestTriageGeschriebenUnveraendert(t *testing.T) {
	m := &fangModell{antwort: `{"action":"answer","text":"Hi!","spoken":"Hi.","details_in_chat":true}`}
	e, err := Triagieren(context.Background(), m, Rahmen{Rolle: "Demo"}, "", nil, nil, nil, "Hallo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.system != triageSystem {
		t.Fatal("the system prompt of a written message changed")
	}
	if strings.Contains(m.prompt, "call") {
		t.Fatalf("the prompt of a written message mentions a call:\n%s", m.prompt)
	}
	if e.Gesprochen != "" || e.DetailsImChat {
		t.Fatalf("a written message kept a spoken form: %+v", e)
	}
	if meta := e.AnswerMeta(); meta[MetaSpoken] != "" || meta[MetaDetailsInChat] != "" {
		t.Fatalf("meta: %v", meta)
	}
}

// TestTriageImAnrufOhneGesprochen: a model that forgot the spoken form still
// answers; the call then speaks the written one.
func TestTriageImAnrufOhneGesprochen(t *testing.T) {
	m := &fangModell{antwort: `{"action":"note","task":"ab12","text":"Gutschrift.","reply":"Danke, nehm ich mit.","details_in_chat":true}`}
	e, err := Triagieren(context.Background(), m, Rahmen{Anruf: true}, "", nil, nil, nil, "Die haben eine Gutschrift geschickt.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.Aktion != AktionNotiz || e.Gesprochen != "" || e.DetailsImChat {
		t.Fatalf("decision: %+v", e)
	}
	if meta := e.AnswerMeta(); meta != nil {
		t.Fatalf("meta without a spoken form: %v", meta)
	}
}

// TestTriageSucheSprichtNicht: a search says nothing, in a call too.
func TestTriageSucheSprichtNicht(t *testing.T) {
	m := &fangModell{antwort: `{"action":"search","query":"Globex","spoken":"Moment."}`}
	e, err := Triagieren(context.Background(), m, Rahmen{Anruf: true}, "", nil, nil, nil, "Was war mit Globex?", nil)
	if err != nil || e.Gesprochen != "" {
		t.Fatalf("search: %+v %v", e, err)
	}
}

// TestWithSpokenKopiert: the meta a decision carries is not changed under
// the other messages it writes.
func TestWithSpokenKopiert(t *testing.T) {
	stimme := map[string]string{"voice_reason": "agent×chat"}
	out := WithSpoken(stimme, "  Ja,\n ist drin. ", false)
	if out[MetaSpoken] != "Ja, ist drin." || out[MetaDetailsInChat] != "" {
		t.Fatalf("meta: %v", out)
	}
	if _, ok := stimme[MetaSpoken]; ok {
		t.Fatal("the decision's own meta was changed")
	}
	if got := WithSpoken(stimme, "  ", true); len(got) != 1 {
		t.Fatalf("an empty spoken form adds nothing: %v", got)
	}
}

// TestSprechbarBegrenzt: a spoken form longer than SpokenMax is cut at a
// sentence end, and markdown is taken out.
func TestSprechbarBegrenzt(t *testing.T) {
	lang := strings.Repeat("Das ist ein Satz für das Ohr. ", 30)
	s := Sprechbar(lang)
	if n := len([]rune(s)); n > SpokenMax || !strings.HasSuffix(s, ".") {
		t.Fatalf("%d runes: %q", n, s)
	}
	if got := Sprechbar("Das ist **fertig**."); got != "Das ist fertig." {
		t.Fatalf("markdown: %q", got)
	}
}

// TestSplitSpoken: the trailing tag a chat answer's run writes in a call is
// taken off the written reply.
func TestSplitSpoken(t *testing.T) {
	res := geschriebenMR + "\n\n<spoken details_in_chat=\"true\">" + gesprochenMR + "</spoken>\n"
	w, g, d := SplitSpoken(res)
	if w != geschriebenMR || g != gesprochenMR || !d {
		t.Fatalf("split: %q | %q | %v", w, g, d)
	}
	w, g, d = SplitSpoken("Hi Ada!\n<spoken>Hi Ada.</spoken>")
	if w != "Hi Ada!" || g != "Hi Ada." || d {
		t.Fatalf("split without details: %q | %q | %v", w, g, d)
	}
	if w, g, _ := SplitSpoken(geschriebenMR); w != geschriebenMR || g != "" {
		t.Fatalf("without a tag: %q | %q", w, g)
	}
}

// fehlerModell fails every call.
type fehlerModell struct{}

func (fehlerModell) Name() string { return "fehler" }
func (fehlerModell) Complete(context.Context, llm.Request) (string, error) {
	return "", errors.New("unavailable")
}

// TestSprechfassung: one short turn on the fast tier makes the spoken form
// of a result; its failure leaves the message as it is written.
func TestSprechfassung(t *testing.T) {
	m := &fangModell{antwort: `Here: {"spoken":"` + gesprochenMR + `","details_in_chat":true}`}
	g, d, err := Sprechfassung(context.Background(), m, geschriebenMR)
	if err != nil || g != gesprochenMR || !d {
		t.Fatalf("spoken form: %q %v %v", g, d, err)
	}
	if !strings.Contains(m.prompt, geschriebenMR) || !strings.Contains(m.system, "one to three short sentences") {
		t.Fatalf("turn: %s\n%s", m.system, m.prompt)
	}
	if _, _, err := Sprechfassung(context.Background(), fehlerModell{}, geschriebenMR); err == nil {
		t.Fatal("a failed turn must say so")
	}
	if _, _, err := Sprechfassung(context.Background(), &fangModell{antwort: `{"spoken":" "}`}, geschriebenMR); err == nil {
		t.Fatal("an empty spoken form must not pass")
	}
}
