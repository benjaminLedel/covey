package voice

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"covey/internal/llm"
	"covey/internal/style"
)

// What models do around JSON: a fence, a sentence before it, a value outside
// the choices. The parser has to take the answer anyway — and refuse the ones
// that carry no card, because the card is the point.
func TestParseDescribedIsTolerantOfWhatModelsWrap(t *testing.T) {
	answer := "Here is the voice:\n```json\n" + `{
  "card": "Opening: the text begins with the observation.\n\nNever: no exclamation marks.",
  "exemplars": [
    {"role": "opening", "text": "Am Montag stand die Auslieferung still."},
    {"role": "Evidence", "text": "  206.000 von 262.000 Ereignissen waren Verlauf.  "},
    {"role": "hook", "text": "Ein Beispiel ist der Agent, der sechs Wochen lief."},
    {"role": "short", "text": ""},
    {"role": "long", "text": "am montag stand die   Auslieferung still."}
  ],
  "chat_tone": {"address": "Sie", "tone": "loud", "emoji": "never", "note": "Kurz."}
}` + "\n```\nHope this helps."
	d, err := ParseDescribed(answer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(d.Card, "Opening:") {
		t.Errorf("card: %q", d.Card)
	}
	// The empty one and the repeated one (case and spacing aside) are gone;
	// an unknown role becomes "passage", a known one is lower-cased.
	if len(d.Exemplars) != 3 {
		t.Fatalf("exemplars: %+v", d.Exemplars)
	}
	if d.Exemplars[1].Role != RoleEvidence || d.Exemplars[1].Text != "206.000 von 262.000 Ereignissen waren Verlauf." {
		t.Errorf("second exemplar: %+v", d.Exemplars[1])
	}
	if d.Exemplars[2].Role != "passage" {
		t.Errorf("an unknown role has to be named neutrally: %+v", d.Exemplars[2])
	}
	// "loud" is not a tone: that field goes, the valid ones stay.
	if d.ChatTone.Address != AddressSie || d.ChatTone.Tone != "" || d.ChatTone.Emoji != EmojiNever || d.ChatTone.Note != "Kurz." {
		t.Errorf("chat tone: %+v", d.ChatTone)
	}
}

func TestParseDescribedRefusesAnAnswerWithoutACard(t *testing.T) {
	for _, answer := range []string{
		"I cannot do that.",
		`{"card": "", "exemplars": [{"role":"opening","text":"x"}]}`,
		`{"card": "A card.", "exemplars": []}`,
		`{"card": "A card.", "exemplars": [`,
	} {
		if _, err := ParseDescribed(answer); err == nil {
			t.Errorf("accepted: %s", answer)
		}
	}
}

func TestParseDescribedKeepsAtMostEightExemplars(t *testing.T) {
	var parts []string
	for i := 0; i < 12; i++ {
		parts = append(parts, fmt.Sprintf(`{"role":"example","text":"Absatz Nummer %d mit Inhalt."}`, i))
	}
	d, err := ParseDescribed(`{"card":"c","exemplars":[` + strings.Join(parts, ",") + `]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Exemplars) != maxExemplars {
		t.Errorf("%d exemplars, want %d — they are paid for in every prompt", len(d.Exemplars), maxExemplars)
	}
}

func TestParseRefinedKeepsExemplarsOnlyForADescribedVoice(t *testing.T) {
	answer := `{"card":"New card.","exemplars":[{"role":"opening","text":"Neu."}]}`
	r, err := ParseRefined(answer, false)
	if err != nil || r.Card != "New card." || r.Exemplars != nil {
		t.Fatalf("a measured voice's exemplars are quotes and stay: %+v %v", r, err)
	}
	r, err = ParseRefined(answer, true)
	if err != nil || len(r.Exemplars) != 1 {
		t.Fatalf("a described voice's exemplars are revised with the card: %+v %v", r, err)
	}
	// A revision that lost the exemplars keeps the old ones (nil = unchanged).
	if r, _ := ParseRefined(`{"card":"c"}`, true); r.Exemplars != nil {
		t.Errorf("no exemplars in the answer must not mean no exemplars: %+v", r)
	}
}

// The description prompt says where the claims come from — the one rule that
// differs from a card written from a corpus.
func TestDescribePromptSaysTheClaimsAreFromTheDescription(t *testing.T) {
	p := DescribePrompt(DescribeInput{Name: "Support", Purpose: PurposeSupportMail, Language: "de",
		Description: "Wir schreiben kurz und siezen."})
	for _, want := range []string{"support mail", "IN DE", "quote none", "Wir schreiben kurz und siezen.", "chat_tone"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q", want)
		}
	}
}

type fakeModel struct {
	answer string
	got    llm.Request
}

func (f *fakeModel) Name() string { return "fake" }
func (f *fakeModel) Complete(_ context.Context, req llm.Request) (string, error) {
	f.got = req
	return f.answer, nil
}

// Every call that writes for a voice is bounded, and the preview runs on the
// fast tier.
func TestVoiceCallsAreBounded(t *testing.T) {
	m := &fakeModel{answer: `{"card":"c","exemplars":[{"role":"opening","text":"t"}]}`}
	if _, err := Describe(context.Background(), m, DescribeInput{Name: "n", Description: "d"}); err != nil {
		t.Fatal(err)
	}
	if m.got.MaxTokens == 0 || m.got.MaxTokens > 4000 {
		t.Errorf("describe max tokens: %d", m.got.MaxTokens)
	}
	m.answer = "  Ein Beispieltext.  "
	text, err := Preview(context.Background(), m, PreviewInput{Card: "c", Topic: "Lieferverzug", Purpose: PurposeChat})
	if err != nil || text != "Ein Beispieltext." {
		t.Fatalf("preview: %q %v", text, err)
	}
	if m.got.Tier != llm.TierFast || m.got.MaxTokens > 1000 {
		t.Errorf("preview: tier %s, max tokens %d", m.got.Tier, m.got.MaxTokens)
	}
	if !strings.Contains(m.got.Messages[0].Content, "chat message") {
		t.Errorf("a chat voice previews as a chat message by default:\n%s", m.got.Messages[0].Content)
	}
	if _, err := Preview(context.Background(), m, PreviewInput{Topic: "x"}); err == nil {
		t.Error("a voice with neither card nor exemplars has nothing to preview in")
	}
	if _, err := Preview(context.Background(), m, PreviewInput{Card: "c"}); err == nil {
		t.Error("a preview needs a topic")
	}
}

// A described voice renders without a profile block — the gate finds none and
// skips — and says in the file that it was described, not measured.
func TestRenderADescribedVoice(t *testing.T) {
	v := Voice{Name: "Support", Language: "de", Source: FromDescription, ReleasedCard: "Die Hand.",
		Exemplars: []Exemplar{{Role: RoleOpening, Text: "Guten Tag, danke für Ihre Nachricht."}}}
	tone := Render(v)
	if !strings.Contains(tone, "described in words rather than measured") {
		t.Errorf("the file has to say where the voice comes from:\n%s", tone)
	}
	if strings.Contains(tone, "built from 0 texts") {
		t.Errorf("a described voice was built from nothing:\n%s", tone)
	}
	if _, _, err := style.ParseProfile(tone); err == nil {
		t.Error("a described voice must carry no profile block")
	}
	if len(style.ParseProfiles(tone)) != 0 {
		t.Error("the gate must find no profile to check against")
	}
	if !v.Assignable() {
		t.Error("a released described voice can be carried")
	}
	v.ReleasedCard = ""
	if v.Assignable() {
		t.Error("an unreleased described voice cannot — everything in it was written by a model")
	}
}

func TestNormalizePurpose(t *testing.T) {
	if p, err := NormalizePurpose(" Support_Mail "); err != nil || p != PurposeSupportMail {
		t.Errorf("%q %v", p, err)
	}
	if p, err := NormalizePurpose(""); err != nil || p != "" {
		t.Errorf("empty: %q %v", p, err)
	}
	if _, err := NormalizePurpose("poetry"); err == nil {
		t.Error("an unknown purpose has to be refused")
	}
}
