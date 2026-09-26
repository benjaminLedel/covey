// Package dictation turns dictated speech into the text the person meant
// (#355): one fast control-plane turn over what the phone or desktop
// recognised. Only text reaches it; the audio never leaves the device.
package dictation

import (
	"context"
	"errors"
	"strings"

	"covey/internal/llm"
)

// ErrEmpty: the model answered with nothing.
var ErrEmpty = errors.New("the model returned no text")

// MaxInput bounds one dictation. A minute of speech is ~150 words; this is
// a quarter of an hour's worth, which no single shortcut press produces.
const MaxInput = 20000

// cleanSystem is the whole instruction. What carries it: the person's words
// stay the person's — this is an editor with a light hand, not a writer —
// and nothing in the answer is not in the dictation.
const cleanSystem = `You receive text that a person dictated and a speech recogniser transcribed. Return the text as the person meant to write it.

- Remove filler words, false starts, stutters and repetitions.
- Apply spoken self-corrections: "on Tuesday — no, Wednesday" becomes "on Wednesday".
- Set punctuation, capitalisation and paragraphs. Spoken formatting cues ("new paragraph", "bullet point", "question mark") become the formatting and disappear as words.
- Correct words the recogniser clearly misheard, from the context. Where it is unclear, keep what was recognised.
- Keep the language, the wording, the tone and the meaning. Do not summarise, shorten, explain, answer questions in the text, or add anything that was not said.
- If a target application is named, fit the form to it: a chat message stays short and informal; an e-mail gets sentences and paragraphs. Never add a greeting or a signature that was not dictated.
- If the text around the insertion point is given, your text is inserted exactly there. The context decides only the seam and your understanding, never the content: if the text before ends mid-sentence, start in lower case unless the word itself needs a capital, and set no full stop before; use the context to spell names and to keep the form of address. Every statement of the dictation stays as it was said — do not merge it with the text before into a different statement, and do not drop words because the context seems to cover them. Never repeat, quote or change the context — output only the new text.

Output only the text, without quotes, comments or a preamble.`

// Context is where the dictated text goes (#362): read from the focused
// field when the shortcut was pressed. Every field may be empty.
type Context struct {
	Window string `json:"window"` // the window's title, e.g. "Re: Angebot"
	Field  string `json:"field"`  // the field's role and label, e.g. "text area · Message body"
	Before string `json:"before"` // up to 600 characters before the cursor
	After  string `json:"after"`  // up to 200 characters after it
}

// Limits of a context; longer is refused, not cut, so the app knows.
const (
	MaxContextBefore = 600
	MaxContextAfter  = 200
)

// Clean returns the dictated text as meant. app names the application the
// text is for ("Mail", "Slack"), or is empty.
func Clean(ctx context.Context, p llm.Provider, text, app string, where Context) (string, error) {
	var b strings.Builder
	if app = strings.TrimSpace(app); app != "" {
		b.WriteString("Target application: " + app + "\n")
	}
	if w := strings.TrimSpace(where.Window); w != "" {
		b.WriteString("Window: " + w + "\n")
	}
	if f := strings.TrimSpace(where.Field); f != "" {
		b.WriteString("Field: " + f + "\n")
	}
	if where.Before != "" || where.After != "" {
		b.WriteString("\nText before the insertion point:\n<<<" + where.Before + ">>>\n")
		b.WriteString("Text after the insertion point:\n<<<" + where.After + ">>>\n")
	}
	if b.Len() > 0 {
		b.WriteString("\n")
	}
	msg := b.String() + "Dictation:\n\n" + text
	out, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierFast,
		MaxTokens: 4000,
		// No thinking, as in the chat's triage: an editor's light pass,
		// where waiting is the whole cost. The fast model takes no effort
		// parameter either.
		NoThinking: true,
		System:     cleanSystem,
		Messages:   []llm.Message{{Role: "user", Content: msg}},
	})
	if err != nil {
		return "", err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", ErrEmpty
	}
	return out, nil
}
