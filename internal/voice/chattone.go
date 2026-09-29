package voice

import (
	"fmt"
	"strings"
)

/* ChatTone is how an agent talks in the team chat (#457) — the part of a voice
 * that a corpus cannot give.
 *
 * The other four artefacts are measured from texts somebody wrote for the
 * record: a blog, a tender, a handbook. Whether an agent says "du" or "Sie" to
 * a colleague, and whether it answers a thank-you with an emoji, is not in
 * any of them — it is a decision of the organisation, like a dress code. So it
 * is set, not built: three choices and a line of free text.
 *
 * It acts in the two cheap turns of the control plane (the triage and the
 * narration, internal/chat), not in the run: what a run writes into a target
 * system keeps its register from TONE.md. */
type ChatTone struct {
	// Address: du | sie | auto. Auto follows how the person writes.
	Address string `json:"address,omitempty"`
	// Tone: casual | matter_of_fact | formal.
	Tone string `json:"tone,omitempty"`
	// Emoji: never | sparingly | freely.
	Emoji string `json:"emoji,omitempty"`
	// Note is free text for what the three choices do not say.
	Note string `json:"note,omitempty"`
	// fallbackAddress is the organisation's du or sie behind a voice's auto:
	// what to use when the person's own address cannot be told.
	fallbackAddress string
}

// The values, as the API and the interface spell them.
const (
	AddressDu   = "du"
	AddressSie  = "sie"
	AddressAuto = "auto"

	ToneCasual       = "casual"
	ToneMatterOfFact = "matter_of_fact"
	ToneFormal       = "formal"

	EmojiNever     = "never"
	EmojiSparingly = "sparingly"
	EmojiFreely    = "freely"
)

// ChatToneNoteMax: the free text is a line, not a second SOUL.md — it goes
// into every turn of every agent that carries it.
const ChatToneNoteMax = 300

// Normalized trims the tone and refuses what is not one of the values. Empty
// fields are allowed: they fall back to the organisation's default.
func (c ChatTone) Normalized() (ChatTone, error) {
	out := ChatTone{
		Address: strings.ToLower(strings.TrimSpace(c.Address)),
		Tone:    strings.ToLower(strings.TrimSpace(c.Tone)),
		Emoji:   strings.ToLower(strings.TrimSpace(c.Emoji)),
		Note:    strings.TrimSpace(c.Note),
	}
	if !oneOf(out.Address, AddressDu, AddressSie, AddressAuto) {
		return ChatTone{}, fmt.Errorf("%w: address is %q, %q or %q", ErrInvalid, AddressDu, AddressSie, AddressAuto)
	}
	if !oneOf(out.Tone, ToneCasual, ToneMatterOfFact, ToneFormal) {
		return ChatTone{}, fmt.Errorf("%w: tone is %q, %q or %q", ErrInvalid, ToneCasual, ToneMatterOfFact, ToneFormal)
	}
	if !oneOf(out.Emoji, EmojiNever, EmojiSparingly, EmojiFreely) {
		return ChatTone{}, fmt.Errorf("%w: emoji is %q, %q or %q", ErrInvalid, EmojiNever, EmojiSparingly, EmojiFreely)
	}
	if n := len([]rune(out.Note)); n > ChatToneNoteMax {
		return ChatTone{}, fmt.Errorf("%w: the note has %d characters, at most %d", ErrInvalid, n, ChatToneNoteMax)
	}
	return out, nil
}

func oneOf(v string, allowed ...string) bool {
	if v == "" {
		return true
	}
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// Empty reports whether nothing is set.
func (c ChatTone) Empty() bool {
	return c.Address == "" && c.Tone == "" && c.Emoji == "" && c.Note == ""
}

// EffectiveChatTone is the tone an agent talks in: its voice's, field by
// field, and the organisation's where the voice leaves one unset — an agent
// without a voice gets the organisation's whole. The free text is not merged:
// a voice that says something of its own says it instead of the default.
func EffectiveChatTone(voice, org ChatTone) ChatTone {
	out := voice
	if out.Address == "" {
		out.Address = org.Address
	}
	if out.Address == AddressAuto && (org.Address == AddressDu || org.Address == AddressSie) {
		out.fallbackAddress = org.Address
	}
	if out.Tone == "" {
		out.Tone = org.Tone
	}
	if out.Emoji == "" {
		out.Emoji = org.Emoji
	}
	if out.Note == "" {
		out.Note = org.Note
	}
	return out
}

// Prompt is the tone as the turns read it: one short block, empty when
// nothing is set. Compact on purpose — it stands in every turn.
func (c ChatTone) Prompt() string {
	var teile []string
	switch c.Address {
	case AddressDu:
		teile = append(teile, `informal address (in German "du")`)
	case AddressSie:
		teile = append(teile, `formal address (in German "Sie")`)
	case AddressAuto:
		s := `address people the way they address you ("du" for "du", "Sie" for "Sie")`
		if c.fallbackAddress != "" {
			s += fmt.Sprintf(`; when you cannot tell, %q`, map[string]string{AddressDu: "du", AddressSie: "Sie"}[c.fallbackAddress])
		}
		teile = append(teile, s)
	}
	switch c.Tone {
	case ToneCasual:
		teile = append(teile, "casual, like with a colleague you know well")
	case ToneMatterOfFact:
		teile = append(teile, "friendly and matter-of-fact")
	case ToneFormal:
		teile = append(teile, "polite and formal, no slang")
	}
	switch c.Emoji {
	case EmojiNever:
		teile = append(teile, "no emoji at all — acknowledge in a word instead")
	case EmojiSparingly:
		teile = append(teile, "an emoji now and then where it fits, at most one")
	case EmojiFreely:
		teile = append(teile, "emoji are welcome, also as a reply on their own")
	}
	if len(teile) == 0 && c.Note == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("How you talk in the team chat (set by your organisation): ")
	b.WriteString(strings.Join(teile, "; "))
	if c.Note != "" {
		if len(teile) > 0 {
			b.WriteString(". ")
		}
		b.WriteString(c.Note)
	}
	return strings.TrimSpace(b.String())
}
