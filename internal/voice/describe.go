package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"covey/internal/llm"
)

// A voice from a description (#458).
//
// Not every organisation has a corpus. Some have a style they can describe and
// have never written down; some have texts nobody would want to be measured
// against. For them a voice is written, not measured: a person says in plain
// words how the organisation writes, and the organisation's model turns that
// into the same card, five to eight exemplars and a suggested chat tone — in
// one call.
//
// What such a voice does NOT have is a profile. Nothing was measured, so there
// are no bands, and the style gate does not check against it: a band invented
// from a description would be a number with a straight face, the thing spec/24
// refuses throughout. It acts while writing, through its card and exemplars,
// and the page says so. As soon as texts are uploaded and it is built, it is
// measured like any other voice.

// Where a voice comes from.
const (
	FromTexts       = "texts"
	FromDescription = "described"
)

// What a voice is for. It goes into every prompt that writes for the voice —
// a card for support mail describes a different move than one for a blog —
// and it picks the preview's default kind.
const (
	PurposeBlog        = "blog"
	PurposeSupportMail = "support_mail"
	PurposeChat        = "chat"
	PurposeOffers      = "offers"
	PurposeOther       = "other"
)

// purposeWords is how a prompt names a purpose.
var purposeWords = map[string]string{
	PurposeBlog:        "blog posts and articles",
	PurposeSupportMail: "support mail to customers",
	PurposeChat:        "chat messages",
	PurposeOffers:      "offers and proposals",
	PurposeOther:       "texts of the organisation",
}

// NormalizePurpose trims a purpose and refuses one that is not in the list.
// Empty is allowed: a voice made before #458 has none.
func NormalizePurpose(p string) (string, error) {
	p = strings.ToLower(strings.TrimSpace(p))
	if p == "" {
		return "", nil
	}
	if _, ok := purposeWords[p]; !ok {
		return "", fmt.Errorf("%w: purpose is one of %s, %s, %s, %s, %s", ErrInvalid,
			PurposeBlog, PurposeSupportMail, PurposeChat, PurposeOffers, PurposeOther)
	}
	return p, nil
}

// DescriptionMax caps the description. It is a few paragraphs of somebody
// saying how they write, not a corpus — a corpus is uploaded.
const DescriptionMax = 4000

// describeMaxTokens bounds the one call: a card of at most 400 words and eight
// short paragraphs, in JSON, with room to spare. A card in German runs to
// about 700 tokens, eight exemplars to about 1500.
const describeMaxTokens = 4000

// Described is what the model makes of a description.
type Described struct {
	Card      string     `json:"card"`
	Exemplars []Exemplar `json:"exemplars"`
	ChatTone  ChatTone   `json:"chat_tone"`
}

// DescribeInput is what a description call needs.
type DescribeInput struct {
	Name        string
	Purpose     string
	Language    string
	Description string
}

// DescribePrompt is what the model is asked. The card follows the same shape
// as the one from a corpus — five headings, 250 to 400 words — so that an
// agent reads the same kind of text whichever source a voice has. The one rule
// that differs is the evidence: a measured card quotes passages, a described
// one can only rest on the description, and it must not pretend otherwise.
func DescribePrompt(in DescribeInput) string {
	var sb strings.Builder
	lang := in.Language
	if lang == "" {
		lang = "the language of the description"
	}
	fmt.Fprintf(&sb, "Below, a person describes in plain words how their organisation writes%s. "+
		"Turn it into a voice an author can write in. Write everything IN %s.\n\n",
		purposePhrase(in.Purpose), strings.ToUpper(lang))
	fmt.Fprintf(&sb, "1. card — a description of the hand, 250 to %d words, continuous prose under these "+
		"five headings: opening, argument, facts, sentences and paragraphs, and never.\n"+
		"   - Every claim comes from the description below. There are no texts to quote, so quote none "+
		"and do not pretend to: where the description says nothing about a heading, say in one sentence "+
		"that it does not, rather than inventing a habit.\n"+
		"   - Describe what the author DOES, so that somebody can write that way — not how it feels.\n"+
		"   - No praise, no advice about writing in general.\n", cardWords)
	fmt.Fprintf(&sb, "2. exemplars — %d to %d short paragraphs (%d to %d words each) written in this voice, "+
		"each on a different everyday subject of such texts, with the role it shows: one of opening, "+
		"evidence, example, long, short, closing. Concrete: names, numbers, a date where the voice "+
		"would have one. None may close on an antithesis (\"…, not …\").\n",
		minExemplars, maxExemplars, minExemplarWords, 120)
	sb.WriteString("3. chat_tone — how this voice would talk to colleagues in a team chat: " +
		`"address" one of "du", "sie", "auto"; "tone" one of "casual", "matter_of_fact", "formal"; ` +
		`"emoji" one of "never", "sparingly", "freely"; "note" at most one short sentence, or empty. ` +
		"Leave a field empty when the description gives no hint.\n\n")
	sb.WriteString("Answer with ONE JSON object and nothing else:\n" +
		`{"card":"…","exemplars":[{"role":"opening","text":"…"}],"chat_tone":{"address":"","tone":"","emoji":"","note":""}}` +
		"\n\n")
	fmt.Fprintf(&sb, "## The description (voice %q)\n\n%s\n", in.Name, strings.TrimSpace(in.Description))
	return sb.String()
}

func purposePhrase(purpose string) string {
	if w, ok := purposeWords[purpose]; ok {
		return " — for " + w
	}
	return ""
}

// Describe asks the organisation's model for card, exemplars and chat tone.
// One call, bounded, no tools.
func Describe(ctx context.Context, p llm.Provider, in DescribeInput) (Described, error) {
	if p == nil {
		return Described{}, llm.ErrNoCredential
	}
	if strings.TrimSpace(in.Description) == "" {
		return Described{}, fmt.Errorf("%w: a described voice needs a description", ErrInvalid)
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierBest,
		MaxTokens:  describeMaxTokens,
		NoThinking: true,
		System: "You turn a description of how an organisation writes into a voice: a card, " +
			"exemplars, a chat tone. You claim only what the description supports. You answer in JSON.",
		Messages: []llm.Message{{Role: "user", Content: DescribePrompt(in)}},
	})
	if err != nil {
		return Described{}, err
	}
	return ParseDescribed(out)
}

// ParseDescribed reads the model's answer. Tolerant of what models do around
// JSON — a fence, a sentence before it — and strict about what goes in: a card
// is required, the exemplars are capped at eight and cleaned, and a chat-tone
// value that is not one of the choices is dropped rather than failing the
// whole answer: the tone is a suggestion a person saves, the card is the point.
func ParseDescribed(raw string) (Described, error) {
	body := jsonObject(raw)
	if body == "" {
		return Described{}, fmt.Errorf("the model's answer holds no JSON object")
	}
	var in struct {
		Card      string          `json:"card"`
		Exemplars []Exemplar      `json:"exemplars"`
		ChatTone  json.RawMessage `json:"chat_tone"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		return Described{}, fmt.Errorf("the model's answer is not readable: %v", err)
	}
	out := Described{Card: strings.TrimSpace(in.Card)}
	if out.Card == "" {
		return Described{}, fmt.Errorf("the model's answer has no card")
	}
	out.Exemplars = cleanExemplars(in.Exemplars)
	if len(out.Exemplars) == 0 {
		return Described{}, fmt.Errorf("the model's answer has no exemplars")
	}
	out.ChatTone = lenientTone(in.ChatTone)
	return out, nil
}

// cleanExemplars trims, drops the empty and the repeated, names an unknown
// role "passage", and keeps at most eight — the same cap as a measured voice,
// for the same reason: they are paid for in every prompt.
func cleanExemplars(in []Exemplar) []Exemplar {
	known := map[string]bool{RoleOpening: true, RoleEvidence: true, RoleExample: true,
		RoleLong: true, RoleShort: true, RoleClosing: true}
	seen := map[string]bool{}
	var out []Exemplar
	for _, ex := range in {
		text := strings.TrimSpace(ex.Text)
		key := strings.Join(strings.Fields(strings.ToLower(text)), " ")
		if text == "" || seen[key] {
			continue
		}
		seen[key] = true
		role := strings.ToLower(strings.TrimSpace(ex.Role))
		if !known[role] {
			role = "passage"
		}
		out = append(out, Exemplar{Role: role, Text: text})
		if len(out) == maxExemplars {
			break
		}
	}
	return out
}

// lenientTone keeps the fields of a suggested tone that are valid, each on
// its own.
func lenientTone(raw json.RawMessage) ChatTone {
	var t ChatTone
	if len(raw) == 0 || json.Unmarshal(raw, &t) != nil {
		return ChatTone{}
	}
	var out ChatTone
	if n, err := (ChatTone{Address: t.Address}).Normalized(); err == nil {
		out.Address = n.Address
	}
	if n, err := (ChatTone{Tone: t.Tone}).Normalized(); err == nil {
		out.Tone = n.Tone
	}
	if n, err := (ChatTone{Emoji: t.Emoji}).Normalized(); err == nil {
		out.Emoji = n.Emoji
	}
	if n, err := (ChatTone{Note: t.Note}).Normalized(); err == nil {
		out.Note = n.Note
	}
	return out
}

// jsonObject cuts the outermost {...} out of a model's answer: past a fence,
// past a sentence before or after it.
func jsonObject(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return ""
	}
	return raw[start : end+1]
}

// --- Preview ---

// The kinds of sample a preview writes.
const (
	KindParagraph = "paragraph"
	KindMail      = "mail"
	KindChat      = "chat"
)

// TopicMax caps the topic of a preview: a line, not a brief.
const TopicMax = 300

// previewMaxTokens bounds a sample: a mail of 180 words is well below it.
const previewMaxTokens = 700

// PreviewInput is one sample to write.
type PreviewInput struct {
	Name      string
	Language  string
	Purpose   string
	Card      string
	Exemplars []Exemplar
	// ChatTone is the effective tone, used for a chat message.
	ChatTone ChatTone
	Topic    string
	Kind     string
	// Audience is the "how to speak with us" block of the department the
	// sample is written to (AudiencePrompt, #471); empty for nobody in
	// particular.
	Audience string
}

// DefaultKind is the kind of sample that fits a purpose.
func DefaultKind(purpose string) string {
	switch purpose {
	case PurposeSupportMail, PurposeOffers:
		return KindMail
	case PurposeChat:
		return KindChat
	}
	return KindParagraph
}

// PreviewPrompt is what the model is asked for one sample.
func PreviewPrompt(in PreviewInput) string {
	var sb strings.Builder
	lang := in.Language
	if lang == "" {
		lang = "the language of the passages"
	}
	length := map[string]string{
		KindParagraph: "one paragraph of 80 to 140 words",
		KindMail:      "one short mail of 100 to 180 words, with salutation and sign-off",
		KindChat:      "one chat message of one to three sentences",
	}[in.Kind]
	fmt.Fprintf(&sb, "Write %s IN %s, in the voice described below, on this topic: %s\n\n",
		length, strings.ToUpper(lang), strings.TrimSpace(in.Topic))
	sb.WriteString("It is a sample of how the voice sounds. Where the voice would name a number, " +
		"a name or a date, use a plausible one. Answer with the text only — no title, no comment.\n\n")
	if in.Purpose != "" {
		fmt.Fprintf(&sb, "The voice is for %s.\n\n", purposeWords[in.Purpose])
	}
	if card := strings.TrimSpace(in.Card); card != "" {
		sb.WriteString("## The hand\n\n" + card + "\n\n")
	}
	if len(in.Exemplars) > 0 {
		sb.WriteString("## Passages in this voice\n\nMatch the movement, not the subject.\n\n")
		for _, ex := range in.Exemplars {
			fmt.Fprintf(&sb, "> %s\n\n", strings.Join(strings.Fields(ex.Text), " "))
		}
	}
	if in.Kind == KindChat {
		if p := in.ChatTone.Prompt(); p != "" {
			sb.WriteString(p + "\n")
		}
	}
	if a := strings.TrimSpace(in.Audience); a != "" {
		sb.WriteString(a + "\n")
	}
	return sb.String()
}

// Preview writes one sample in a voice. Not stored: it is for a person to hear
// the voice before an agent carries it. The fast tier — a sample is short, and
// waiting is what a preview costs.
func Preview(ctx context.Context, p llm.Provider, in PreviewInput) (string, error) {
	if p == nil {
		return "", llm.ErrNoCredential
	}
	if strings.TrimSpace(in.Topic) == "" {
		return "", fmt.Errorf("%w: a preview needs a topic", ErrInvalid)
	}
	if n := len([]rune(in.Topic)); n > TopicMax {
		return "", fmt.Errorf("%w: the topic has %d characters, at most %d", ErrInvalid, n, TopicMax)
	}
	switch in.Kind {
	case "":
		in.Kind = DefaultKind(in.Purpose)
	case KindParagraph, KindMail, KindChat:
	default:
		return "", fmt.Errorf("%w: a preview is a %q, a %q or a %q", ErrInvalid, KindParagraph, KindMail, KindChat)
	}
	if strings.TrimSpace(in.Card) == "" && len(in.Exemplars) == 0 {
		return "", fmt.Errorf("%w: the voice has neither card nor exemplars yet — nothing to write in", ErrInvalid)
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierFast,
		MaxTokens:  previewMaxTokens,
		NoThinking: true,
		System:     "You write short sample texts in a given voice. You answer with the text only.",
		Messages:   []llm.Message{{Role: "user", Content: PreviewPrompt(in)}},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// --- Refine ---

// InstructionMax caps a refinement: an instruction, not a new description.
const InstructionMax = 1000

// RefineInput is one revision by instruction.
type RefineInput struct {
	Name        string
	Language    string
	Purpose     string
	Card        string
	Exemplars   []Exemplar
	Instruction string
	// Described: the exemplars were written by a model and are revised with
	// the card. A measured voice's exemplars are quotes and stay as they are.
	Described bool
}

// Refined is the revised draft. Exemplars is nil when they were not revised.
type Refined struct {
	Card      string
	Exemplars []Exemplar
}

// RefinePrompt is what the model is asked.
func RefinePrompt(in RefineInput) string {
	var sb strings.Builder
	sb.WriteString("Revise the description of a voice by the instruction below. Keep what the instruction " +
		"does not touch, word for word where you can; keep the language, the five headings and the length " +
		fmt.Sprintf("(250 to %d words). Quoted passages stay quoted; add no quote that was not there.\n\n", cardWords))
	fmt.Fprintf(&sb, "## Instruction\n\n%s\n\n", strings.TrimSpace(in.Instruction))
	if in.Purpose != "" {
		fmt.Fprintf(&sb, "The voice (%q) is for %s.\n\n", in.Name, purposeWords[in.Purpose])
	}
	sb.WriteString("## The card\n\n" + strings.TrimSpace(in.Card) + "\n\n")
	if in.Described {
		sb.WriteString("## The exemplars\n\nRewrite them as well where the instruction concerns them, " +
			"keeping their roles and their number.\n\n")
		raw, _ := json.Marshal(in.Exemplars)
		sb.Write(raw)
		sb.WriteString("\n\nAnswer with ONE JSON object and nothing else: " +
			`{"card":"…","exemplars":[{"role":"…","text":"…"}]}` + "\n")
	} else {
		sb.WriteString("Answer with ONE JSON object and nothing else: " + `{"card":"…"}` + "\n")
	}
	return sb.String()
}

// Refine revises a card — and a described voice's exemplars — by instruction.
// The result is a draft: it acts once a person releases it, like every card.
func Refine(ctx context.Context, p llm.Provider, in RefineInput) (Refined, error) {
	if p == nil {
		return Refined{}, llm.ErrNoCredential
	}
	if strings.TrimSpace(in.Instruction) == "" {
		return Refined{}, fmt.Errorf("%w: say what should change", ErrInvalid)
	}
	if n := len([]rune(in.Instruction)); n > InstructionMax {
		return Refined{}, fmt.Errorf("%w: the instruction has %d characters, at most %d", ErrInvalid, n, InstructionMax)
	}
	if strings.TrimSpace(in.Card) == "" {
		return Refined{}, fmt.Errorf("%w: there is no card to refine yet", ErrInvalid)
	}
	out, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierBest,
		MaxTokens:  describeMaxTokens,
		NoThinking: true,
		System:     "You revise the description of a writing voice by an instruction. You answer in JSON.",
		Messages:   []llm.Message{{Role: "user", Content: RefinePrompt(in)}},
	})
	if err != nil {
		return Refined{}, err
	}
	return ParseRefined(out, in.Described)
}

// ParseRefined reads a revision: a card always, exemplars when asked for. A
// revision that loses the exemplars keeps the old ones rather than none.
func ParseRefined(raw string, described bool) (Refined, error) {
	body := jsonObject(raw)
	if body == "" {
		return Refined{}, fmt.Errorf("the model's answer holds no JSON object")
	}
	var in struct {
		Card      string     `json:"card"`
		Exemplars []Exemplar `json:"exemplars"`
	}
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		return Refined{}, fmt.Errorf("the model's answer is not readable: %v", err)
	}
	out := Refined{Card: strings.TrimSpace(in.Card)}
	if out.Card == "" {
		return Refined{}, fmt.Errorf("the model's answer has no card")
	}
	if described {
		if ex := cleanExemplars(in.Exemplars); len(ex) > 0 {
			out.Exemplars = ex
		}
	}
	return out, nil
}
