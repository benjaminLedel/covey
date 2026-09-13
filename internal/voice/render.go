package voice

import (
	"encoding/json"
	"fmt"
	"strings"

	"covey/internal/style"
)

// Render writes a voice as the TONE.md an agent carries.
//
// The file has two halves and they are read by two different things. The prose
// goes into the compiled system prompt (agents.CompilePrompt keeps every .md of
// the config, with the profile block stripped), so it is what the model has in
// front of it while WRITING. The ```style-profile``` block at the end is what
// the style gate and the revision service measure against afterwards.
//
// That is why the card and the exemplars sit in the prose and the bands do not:
// a band is a grade, and a grade steers nothing while a text is being written.
func Render(v Voice) string {
	var b strings.Builder
	b.WriteString("# Tone\n\n")
	fmt.Fprintf(&b, "This is the voice %q", v.Name)
	if v.Language != "" {
		fmt.Fprintf(&b, " (%s)", v.Language)
	}
	fmt.Fprintf(&b, ", built from %d texts, %d words. Write in it.\n\n", v.Documents, v.Words)

	if card := strings.TrimSpace(v.ReleasedCard); card != "" {
		b.WriteString("## The hand\n\n")
		b.WriteString(card)
		b.WriteString("\n\n")
	}

	if len(v.Exemplars) > 0 {
		b.WriteString("## Passages of this author\n\n" +
			"Not material to reuse, and not a topic: this is what the movement looks like. " +
			"Match it — the opening, how a fact is brought in, how long a paragraph runs.\n\n")
		for _, ex := range v.Exemplars {
			fmt.Fprintf(&b, "**%s**\n\n> %s\n\n", ex.Role, blockquote(ex.Text))
		}
	}

	if len(v.Contrast) > 0 {
		b.WriteString("## What this author does not do\n\n" +
			"Measured against a corpus of AI text. These are the habits that mark a draft as " +
			"written by a model rather than by this person:\n\n")
		for _, c := range v.Contrast {
			fmt.Fprintf(&b, "- **%s** — this author %.2f, a model %.2f.\n", label(c), c.Author, c.Other)
		}
		b.WriteString("\n")
	}

	if block, err := profileBlock(v.Profile); err == nil {
		b.WriteString(block)
	}
	return b.String()
}

// blockquote keeps a passage inside one Markdown quote — an empty line would
// end the quote and leave the rest standing as the agent's own instruction.
func blockquote(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for i, l := range lines {
		if i == 0 {
			continue
		}
		lines[i] = "> " + strings.TrimSpace(l)
	}
	return strings.Join(lines, "\n")
}

func profileBlock(p style.Profile) (string, error) {
	if len(p.Bands) == 0 {
		return "", fmt.Errorf("no bands")
	}
	out, err := json.MarshalIndent(p, "", " ")
	if err != nil {
		return "", err
	}
	return "```style-profile\n" + string(out) + "\n```\n", nil
}
