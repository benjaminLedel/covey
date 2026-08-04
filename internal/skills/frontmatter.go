package skills

import (
	"strings"
)

// Frontmatter handling of SKILL.md.
//
// Claude Code expects a YAML block between --- at the start of SKILL.md, whose
// `description` decides whether the skill is loaded. Covey nevertheless keeps
// the description as a COLUMN and the SKILL.md without frontmatter: lists, UI
// and bundle need the description without parsing files, and the description
// must not be able to contradict itself in two places.
//
// That leaves exactly two transitions: while materializing the frontmatter is
// generated (Render), on import from a bundle or the editor it is stripped
// (SplitEntry). No YAML parser — the block has two scalar keys, and a
// dependency for that would be out of proportion.

// Render builds the SKILL.md that lands on disk: frontmatter from name and
// description, then the stored body.
func Render(name, description, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlScalar(name) + "\n")
	b.WriteString("description: " + yamlScalar(description) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// yamlScalar makes a single-line YAML scalar safe. Descriptions regularly
// contain colons ("Use this when: …") — unquoted that would be a YAML map and
// the frontmatter broken.
func yamlScalar(v string) string {
	v = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " "))
	if v == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(v, `:#"'{}[]&*!|>%@`+"`") ||
		strings.HasPrefix(v, "-") || strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ")
	if !needsQuote {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// SplitEntry separates an incoming SKILL.md into frontmatter values and body.
// If the frontmatter is missing, everything is body and the values stay empty —
// the caller then uses its own (form field, bundle field).
//
// Returns: name and description from the block (empty if unset), body without
// the block.
func SplitEntry(content string) (name, description, body string) {
	// \ufeff is the BOM some editors prepend — without swallowing it the
	// file does not start with --- and the frontmatter would be missed.
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", content
	}
	// Find the line after the opening ---.
	rest := trimmed[len("---"):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		return "", "", content
	}
	end := findCloser(rest)
	if !end.valid() {
		return "", "", content // unterminated block: do not guess, everything is body
	}
	block, after := rest[:end.start], rest[end.after:]
	for _, line := range strings.Split(block, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "name":
			name = unquote(val)
		case "description":
			description = unquote(val)
		}
	}
	return name, description, strings.TrimLeft(after, "\n")
}

// UnsupportedFrontmatterKeys returns the frontmatter keys of a SKILL.md that
// Covey does not track — empty when only name/description (or no block at all)
// occur.
//
// The caller uses this to reject rather than store. Reason: SplitEntry cuts off
// the whole block and Render rebuilds it from name and description — anything
// else would be gone without a trace after saving. With `allowed-tools:` that
// means the materialized SKILL.md lands in the home without the restriction, so
// the skill runs with MORE permissions than its author wrote. A silent loss
// nobody notices; a rejection everybody sees right away.
func UnsupportedFrontmatterKeys(content string) []string {
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return nil
	}
	rest := trimmed[len("---"):]
	i := strings.IndexByte(rest, '\n')
	if i < 0 {
		return nil
	}
	rest = rest[i+1:]
	end := findCloser(rest)
	if !end.valid() {
		return nil // an unterminated block is body, see SplitEntry
	}
	var out []string
	for _, line := range strings.Split(rest[:end.start], "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch k := strings.TrimSpace(key); k {
		case "name", "description":
		default:
			// Continuation lines of a list ("  - Bash") carry no key of their
			// own — they belong to the key above, which is already reported.
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") && !strings.HasPrefix(k, "-") {
				out = append(out, k)
			}
		}
	}
	return out
}

// closer describes where the closing --- was found.
type closer struct{ start, after int }

func (c closer) valid() bool { return c.start >= 0 }

// findCloser looks for the line that consists of --- alone.
func findCloser(s string) closer {
	offset := 0
	for offset <= len(s) {
		nl := strings.IndexByte(s[offset:], '\n')
		var line string
		var next int
		if nl < 0 {
			line, next = s[offset:], len(s)
		} else {
			line, next = s[offset:offset+nl], offset+nl+1
		}
		if strings.TrimSpace(line) == "---" {
			return closer{start: offset, after: next}
		}
		if nl < 0 {
			break
		}
		offset = next
	}
	return closer{start: -1}
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"') {
		inner := v[1 : len(v)-1]
		return strings.ReplaceAll(strings.ReplaceAll(inner, `\"`, `"`), `\\`, `\`)
	}
	if len(v) >= 2 && (v[0] == '\'' && v[len(v)-1] == '\'') {
		return strings.ReplaceAll(v[1:len(v)-1], `''`, `'`)
	}
	return v
}
