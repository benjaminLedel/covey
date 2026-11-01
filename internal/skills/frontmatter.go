package skills

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Frontmatter handling of SKILL.md.
//
// The YAML block at the start of SKILL.md is defined by the Agent Skills format
// (https://agentskills.io/specification): `name` and `description` are required,
// `license`, `compatibility`, `metadata` and `allowed-tools` optional. covey
// keeps all six as COLUMNS and the stored SKILL.md without the block: lists, UI
// and bundle need them without parsing files, and a value must not be able to
// contradict itself in two places.
//
// That leaves exactly two transitions: while materializing the block is
// generated (Render, and the daemon's renderSkillEntry from RenderExtra), on
// import from a bundle, an archive or the editor it is parsed and stripped
// (ParseEntry).

// Meta holds the optional frontmatter fields of the specification.
type Meta struct {
	// License: a licence name or a reference to a bundled licence file.
	License string `json:"license,omitempty"`
	// Compatibility: environment requirements (product, system packages,
	// network access). At most 500 characters by the specification.
	Compatibility string `json:"compatibility,omitempty"`
	// AllowedTools: space-separated tools the skill pre-approves (experimental
	// in the specification). Passed through verbatim; the run's tool scope is
	// set by covey and is not widened by it.
	AllowedTools string `json:"allowed_tools,omitempty"`
	// Metadata: a string-to-string map for anything else (author, version).
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Front is everything the frontmatter block carries.
type Front struct {
	Name        string
	Description string
	Meta
}

// frontKeys are the keys of the specification, in the order Render writes them.
var frontKeys = []string{"name", "description", "license", "compatibility", "allowed-tools", "metadata"}

// Render builds the SKILL.md that lands on disk: frontmatter from the stored
// fields, then the stored body.
func Render(name, description string, meta Meta, body string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + yamlScalar(name) + "\n")
	b.WriteString("description: " + yamlScalar(description) + "\n")
	b.WriteString(RenderExtra(meta))
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// RenderExtra renders the optional fields as frontmatter lines, ready to sit
// between description and the closing ---. The daemon receives this string
// rather than the fields: it writes SKILL.md, and it should not need to know
// the format's field list or carry a YAML library into the sandbox.
func RenderExtra(meta Meta) string {
	var b strings.Builder
	if v := strings.TrimSpace(meta.License); v != "" {
		b.WriteString("license: " + yamlScalar(v) + "\n")
	}
	if v := strings.TrimSpace(meta.Compatibility); v != "" {
		b.WriteString("compatibility: " + yamlScalar(v) + "\n")
	}
	if v := strings.TrimSpace(meta.AllowedTools); v != "" {
		b.WriteString("allowed-tools: " + yamlScalar(v) + "\n")
	}
	if len(meta.Metadata) > 0 {
		keys := make([]string, 0, len(meta.Metadata))
		for k := range meta.Metadata {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("metadata:\n")
		for _, k := range keys {
			b.WriteString("  " + yamlScalar(k) + ": " + yamlScalar(meta.Metadata[k]) + "\n")
		}
	}
	return b.String()
}

// yamlScalar makes a single-line YAML scalar safe. Descriptions regularly
// contain colons ("Use this when: …") — unquoted that would be a YAML map and
// the frontmatter broken. Values that YAML would read as something other than
// a string (true, 1.0, null) are quoted as well, so they come back as written.
func yamlScalar(v string) string {
	v = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " "))
	if v == "" {
		return `""`
	}
	needsQuote := strings.ContainsAny(v, `:#"'{}[]&*!|>%@,`+"`") ||
		strings.HasPrefix(v, "-") || strings.HasPrefix(v, "?") || !readsAsString(v)
	if !needsQuote {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}

// readsAsString tells whether YAML reads the plain scalar v back as the string v.
func readsAsString(v string) bool {
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(v), &n); err != nil || len(n.Content) != 1 {
		return false
	}
	return n.Content[0].Kind == yaml.ScalarNode && n.Content[0].Tag == "!!str" && n.Content[0].Value == v
}

// ParseEntry separates an incoming SKILL.md into frontmatter and body.
//
// Without a block — or with an unterminated one, which is not guessed at —
// everything is body and Front stays empty; the caller then uses its own values
// (form fields, bundle fields).
//
// A key outside the specification is an error, not something to drop: the block
// is rebuilt from the columns on every run, so an unknown key would be gone
// without a trace after saving. With a runtime-specific key such as `hooks:` or
// `model:` that changes what the skill does; a rejection everybody sees beats a
// silent loss nobody notices.
func ParseEntry(content string) (Front, string, error) {
	block, body, ok := cutBlock(content)
	if !ok {
		return Front{}, content, nil
	}
	var front Front
	if strings.TrimSpace(block) == "" {
		return front, body, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(block), &doc); err != nil {
		return Front{}, "", fmt.Errorf("%w: %s frontmatter is not valid YAML: %v", ErrInvalid, EntryFile, err)
	}
	if len(doc.Content) == 0 {
		return front, body, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return Front{}, "", fmt.Errorf("%w: %s frontmatter must be a map of keys", ErrInvalid, EntryFile)
	}
	var unknown []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		key, val := root.Content[i].Value, root.Content[i+1]
		var err error
		switch key {
		case "name":
			front.Name, err = scalarOf(key, val)
		case "description":
			front.Description, err = scalarOf(key, val)
		case "license":
			front.License, err = scalarOf(key, val)
		case "compatibility":
			front.Compatibility, err = scalarOf(key, val)
		case "allowed-tools":
			front.AllowedTools, err = toolsOf(val)
		case "metadata":
			front.Metadata, err = metadataOf(val)
		default:
			unknown = append(unknown, key)
		}
		if err != nil {
			return Front{}, "", err
		}
	}
	if len(unknown) > 0 {
		return Front{}, "", fmt.Errorf(
			"%w: %s frontmatter carries %s — the Agent Skills format defines only %s, and covey keeps nothing else",
			ErrInvalid, EntryFile, strings.Join(unknown, ", "), strings.Join(frontKeys, ", "))
	}
	return front, body, nil
}

func scalarOf(key string, n *yaml.Node) (string, error) {
	if n.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%w: frontmatter key %s must be a single value", ErrInvalid, key)
	}
	if n.Tag == "!!null" {
		return "", nil
	}
	return strings.TrimSpace(n.Value), nil
}

// toolsOf reads allowed-tools. The specification writes it as one
// space-separated string; Claude Code also accepts a YAML list, which is
// joined into the specification's form.
func toolsOf(n *yaml.Node) (string, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return scalarOf("allowed-tools", n)
	case yaml.SequenceNode:
		parts := make([]string, 0, len(n.Content))
		for _, item := range n.Content {
			v, err := scalarOf("allowed-tools", item)
			if err != nil {
				return "", err
			}
			if v != "" {
				parts = append(parts, v)
			}
		}
		return strings.Join(parts, " "), nil
	}
	return "", fmt.Errorf("%w: frontmatter key allowed-tools must be a string or a list", ErrInvalid)
}

func metadataOf(n *yaml.Node) (map[string]string, error) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("%w: frontmatter key metadata must be a map of strings", ErrInvalid)
	}
	out := make(map[string]string, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i].Value
		v, err := scalarOf("metadata."+k, n.Content[i+1])
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

// cutBlock finds the frontmatter block. ok is false when there is none or it
// is not terminated.
func cutBlock(content string) (block, body string, ok bool) {
	// \ufeff is the BOM some editors prepend — without swallowing it the
	// file does not start with --- and the frontmatter would be missed.
	trimmed := strings.TrimLeft(content, "\ufeff \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return "", content, false
	}
	rest := trimmed[len("---"):]
	i := strings.IndexByte(rest, '\n')
	if i < 0 || strings.TrimSpace(rest[:i]) != "" {
		return "", content, false
	}
	rest = rest[i+1:]
	end := findCloser(rest)
	if !end.valid() {
		return "", content, false // unterminated block: do not guess, everything is body
	}
	return strings.ReplaceAll(rest[:end.start], "\r", ""), strings.TrimLeft(rest[end.after:], "\r\n"), true
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
