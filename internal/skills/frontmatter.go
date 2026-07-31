package skills

import (
	"strings"
)

// Frontmatter-Behandlung der SKILL.md.
//
// Claude Code erwartet am Anfang der SKILL.md einen YAML-Block zwischen ---,
// aus dem `description` entscheidet, ob der Skill geladen wird. Covey hält die
// Beschreibung trotzdem als SPALTE und die SKILL.md ohne Frontmatter: Listen,
// UI und Bundle brauchen die Beschreibung, ohne Dateien zu parsen, und die
// Beschreibung soll sich nicht an zwei Orten widersprechen können.
//
// Damit gibt es genau zwei Übergänge: Beim Materialisieren wird der
// Frontmatter erzeugt (Render), beim Import aus einem Bundle oder Editor
// abgeschnitten (SplitEntry). Kein YAML-Parser — der Block hat zwei skalare
// Schlüssel, und eine Abhängigkeit dafür wäre unverhältnismäßig.

// Render baut die SKILL.md, die auf Platte landet: Frontmatter aus Name und
// Beschreibung, danach der gespeicherte Rumpf.
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

// yamlScalar macht einen einzeiligen YAML-Skalar sicher. Beschreibungen
// enthalten regelmäßig Doppelpunkte („Nutze dies, wenn: …") — unquoted wäre
// das eine YAML-Map und der Frontmatter kaputt.
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

// SplitEntry trennt eine eingehende SKILL.md in Frontmatter-Werte und Rumpf.
// Fehlt der Frontmatter, ist alles Rumpf und die Werte bleiben leer — der
// Aufrufer nimmt dann seine eigenen (Formularfeld, Bundle-Feld).
//
// Rückgabe: name und description aus dem Block (leer, wenn nicht gesetzt),
// body ohne den Block.
func SplitEntry(content string) (name, description, body string) {
	// \ufeff ist die BOM, die manche Editoren voranstellen — ohne sie zu
	// schlucken beginnt die Datei nicht mit --- und der Frontmatter würde
	// übersehen.
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", "", content
	}
	// Zeile nach dem öffnenden --- suchen.
	rest := trimmed[len("---"):]
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		return "", "", content
	}
	end := findCloser(rest)
	if !end.valid() {
		return "", "", content // unabgeschlossener Block: nicht raten, alles ist Rumpf
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

// closer beschreibt die Fundstelle des schließenden ---.
type closer struct{ start, after int }

func (c closer) valid() bool { return c.start >= 0 }

// findCloser sucht die Zeile, die nur aus --- besteht.
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
