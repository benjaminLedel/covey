package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/target/manifestplug"
	"covey/internal/target/wasmplug"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// `covey plugin lint` is the check a plugin author runs before opening a pull
// request, and the same one this binary applies at install time. It lives in
// the binary on purpose: a lint that exists only inside somebody else's
// pipeline is one you find out about too late. So it has to work with nothing
// but the binary and the file — no database, no key, no network.
const validManifest = `{
  "name": "helpdesk",
  "label": "Helpdesk",
  "category": "ticketing",
  "auth": {"header": "X-API-Key", "format": "{token}"},
  "scopes": ["read", "write"],
  "webhook": {"signature": "hmac-sha256", "id_field": "issue.id", "title_field": "issue.title"},
  "actions": {
    "get_issue": {"method": "GET", "path": "/issues/{issue_id}", "scope": "read", "doc": "reads one issue"},
    "comment": {"method": "POST", "path": "/issues/{issue_id}/comments", "scope": "write", "doc": "answers"}
  },
  "prompt_doc": "Available helpdesk actions: get_issue, comment."
}`

func write(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPluginLintAcceptsAManifest(t *testing.T) {
	if err := runPlugin([]string{"lint", write(t, "helpdesk.json", validManifest)}); err != nil {
		t.Fatalf("a valid manifest was rejected: %v", err)
	}
}

// A file that is not a plugin has to be named as such, and the exit has to
// carry how many of them there were — an upgrade script reads that.
func TestPluginLintRejectsAndCounts(t *testing.T) {
	bad := write(t, "kaputt.json", `{"name":"Bad"}`)
	err := runPlugin([]string{"lint", bad})
	if err == nil {
		t.Fatal("an invalid manifest passed the lint")
	}
	if !strings.Contains(err.Error(), "1 of 1") {
		t.Errorf("the error does not count the files: %v", err)
	}

	good := write(t, "helpdesk.json", validManifest)
	if err := runPlugin([]string{"lint", good, bad}); err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Errorf("mixed files: %v", err)
	}
}

func TestPluginLintUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"prüfen", "x"}, {"lint"}} {
		if err := runPlugin(args); err == nil {
			t.Errorf("%v was accepted", args)
		}
	}
	if err := runPlugin([]string{"lint", filepath.Join(t.TempDir(), "gibtesnicht.json")}); err == nil {
		t.Error("a file that is not there passed the lint")
	}
}

// The file decides what it is, by shape rather than by a flag — so there is
// one source of truth about a plugin's kind, and it is the plugin.
func TestKindOf(t *testing.T) {
	if got := kindOf([]byte("\x00asm\x01\x00\x00\x00")); got != "wasm" {
		t.Errorf("the wasm magic was not recognised: %q", got)
	}
	if got := kindOf([]byte(`{"url":"https://mcp.example.test","name":"x"}`)); got != "mcp" {
		t.Errorf("an MCP config was read as %q", got)
	}
	// An endpoint AND actions is a manifest — the url alone does not make it
	// an MCP config.
	if got := kindOf([]byte(`{"url":"https://x.test","actions":{"a":{}}}`)); got != "custom" {
		t.Errorf("a manifest with a url was read as %q", got)
	}
	if got := kindOf([]byte(validManifest)); got != "custom" {
		t.Errorf("a manifest was read as %q", got)
	}
	if got := kindOf([]byte("kein json")); got != "custom" {
		t.Errorf("unreadable content was read as %q", got)
	}
}

func TestCategoryFallsBackToOther(t *testing.T) {
	if got := category(""); got != target.CategoryOther {
		t.Errorf("category(\"\") = %q", got)
	}
	if got := category("ticketing"); got != "ticketing" {
		t.Errorf("category was rewritten: %q", got)
	}
}

// The notes are what a plugin gives up. They are printed rather than failed on
// — a plugin without a webhook is legitimate, it just wakes differently — but
// somebody installing it wants to read them first.
func TestManifestNotesNameWhatIsMissing(t *testing.T) {
	m, err := manifestplug.Parse([]byte(`{
	  "name": "knapp",
	  "webhook": {"id_field": "id"},
	  "actions": {"a": {"method": "GET", "path": "/x"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	notes := manifestNotes(m, manifestplug.New(m))
	joined := strings.Join(notes, "\n")
	for _, want := range []string{"no probe:", "no poll:", "no scopes:"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the notes do not mention %q:\n%s", want, joined)
		}
	}

	// Scopes declared and never used: the vocabulary exists and narrows
	// nothing, which is worth saying because ACCESS.md still accepts it.
	m2, err := manifestplug.Parse([]byte(`{
	  "name": "unbenutzt",
	  "scopes": ["read"],
	  "webhook": {"id_field": "id"},
	  "actions": {"a": {"method": "GET", "path": "/x"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(manifestNotes(m2, manifestplug.New(m2)), "\n"), "narrows nothing") {
		t.Error("declared but unused scopes were not named")
	}

	// Scoped actions without doc lines: free text cannot be narrowed, so every
	// agent still gets the whole prompt doc.
	m3, err := manifestplug.Parse([]byte(`{
	  "name": "undokumentiert",
	  "scopes": ["read"],
	  "webhook": {"id_field": "id"},
	  "actions": {"a": {"method": "GET", "path": "/x", "scope": "read"}}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(manifestNotes(m3, manifestplug.New(m3)), "\n"), "doc:") {
		t.Error("scoped actions without a doc line were not named")
	}
}

// A workspace read and a missing webhook signature are the two notes somebody
// wants BEFORE installing, not after.
func TestWasmNotesNameThePrivileges(t *testing.T) {
	bare := wasmNotes(wasmplug.Description{}, 1024)
	joined := strings.Join(bare, "\n")
	for _, want := range []string{"no probe", "no poll", "no scopes", "no webhook"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the notes do not mention %q:\n%s", want, joined)
		}
	}

	d := wasmplug.Description{
		Probe: true, Poll: true, Scopes: []string{"read"},
		Workdir: true,
		Webhook: &wasmplug.WebhookDesc{},
		Actions: []wasmplug.ActionDesc{{Name: "a"}, {Name: "b", Doc: "tut etwas"}},
	}
	joined = strings.Join(wasmNotes(d, 8<<20), "\n")
	for _, want := range []string{
		"reads files out of the agent's checkout",
		"anyone who knows the URL",
		"1 action(s) without a doc line",
		"TinyGo",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the notes do not mention %q:\n%s", want, joined)
		}
	}

	// A module below the size threshold says nothing about its size.
	if strings.Contains(strings.Join(wasmNotes(d, 1024), "\n"), "TinyGo") {
		t.Error("a small module was reported as too large")
	}
}

// An MCP config is the third kind the lint recognises, and the one where the
// note matters most: the endpoint it names is what installing it opens the
// organisation's egress to, so a reviewer has to see that before deciding.
func TestPluginLintAcceptsAnMCPConfigAndNamesItsEndpoint(t *testing.T) {
	path := write(t, "werkzeug.json",
		`{"name":"hauswerkzeug","label":"Hauswerkzeug","url":"https://mcp.example.test/rpc"}`)

	out := captureStdout(t, func() {
		if err := runPlugin([]string{"lint", path}); err != nil {
			t.Fatalf("a valid MCP config was rejected: %v", err)
		}
	})
	if !strings.Contains(out, "mcp plugin") {
		t.Errorf("the lint did not recognise it as MCP:\n%s", out)
	}
	if !strings.Contains(out, "https://mcp.example.test/rpc") {
		t.Errorf("the endpoint is not named:\n%s", out)
	}
	if !strings.Contains(out, "opens the egress") {
		t.Errorf("the note about the egress is missing:\n%s", out)
	}
}

// A config that calls itself MCP and names no usable endpoint is refused: the
// endpoint is the one thing such a plugin consists of.
func TestPluginLintRefusesAnMCPConfigWithoutAUsableEndpoint(t *testing.T) {
	for _, body := range []string{
		`{"name":"ohne-schema","url":"mcp.example.test"}`,
		`{"name":"Falsch Geschrieben","url":"https://mcp.example.test"}`,
	} {
		if err := runPlugin([]string{"lint", write(t, "x.json", body)}); err == nil {
			t.Errorf("%s passed the lint", body)
		}
	}
}
