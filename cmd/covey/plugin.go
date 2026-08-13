package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"covey/internal/target"
	"covey/internal/target/mcp"
)

// runPlugin implements `covey plugin lint <file>…` — the check an author runs
// before opening a pull request against a plugin catalogue, and the same one
// this binary applies when the plugin is installed.
//
// It lives in the binary rather than in a catalogue's CI for two reasons. The
// check IS ParseManifest — reimplementing it elsewhere would mean two
// implementations of "what is a valid plugin", and the one that diverges is
// always the copy. And an author has to be able to run it: a lint that exists
// only inside somebody else's pipeline is one you find out about too late.
//
// It needs no database, no master key and no network — deliberately, so it can
// run in a CI container that has nothing but the binary and the file.
func runPlugin(args []string) error {
	if len(args) == 0 || args[0] != "lint" {
		return fmt.Errorf("usage: covey plugin lint <file>…")
	}
	files := args[1:]
	if len(files) == 0 {
		return fmt.Errorf("usage: covey plugin lint <file>…")
	}
	var failed int
	for _, path := range files {
		if err := lintFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed++
			continue
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d file(s) rejected", failed, len(files))
	}
	return nil
}

func lintFile(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	switch kindOf(raw) {
	case "mcp":
		c, err := mcp.ParseConfig(raw)
		if err != nil {
			return err
		}
		fmt.Printf("%s: ok — mcp plugin %q, endpoint %s, %d tool(s) cached\n",
			path, c.Name, c.URL, len(c.Tools))
		// The endpoint is the one thing an MCP plugin names itself, so it is
		// the one thing a reviewer has to look at: installing it means opening
		// an organization's egress to that host.
		fmt.Printf("%s: note — installing this opens the egress to %s\n", path, c.URL)
		return nil
	default:
		m, err := target.ParseManifest(raw)
		if err != nil {
			return err
		}
		sys := target.NewManifestSystem(m)
		actions := make([]string, 0, len(m.Actions))
		for name := range m.Actions {
			actions = append(actions, name)
		}
		sort.Strings(actions)
		fmt.Printf("%s: ok — manifest plugin %q (%s), %d action(s): %s\n",
			path, m.Name, category(m.Category), len(actions), strings.Join(actions, ", "))
		for _, note := range manifestNotes(m, sys) {
			fmt.Printf("%s: note — %s\n", path, note)
		}
		return nil
	}
}

// manifestNotes are the things that are not errors but cost the plugin
// something: a capability it could declare and does not. They are printed, not
// failed on — a plugin without a webhook is a legitimate plugin, it just wakes
// differently.
func manifestNotes(m target.Manifest, sys *target.ManifestSystem) []string {
	var notes []string
	if !sys.Supports(target.CapProbe) {
		notes = append(notes, `no probe: — the store cannot offer a connection test, so "saved" and "works" stay two different things until an agent runs`)
	}
	if !sys.Supports(target.CapPoll) {
		notes = append(notes, "no poll: — nur-wenn: on this system cannot be answered and every heartbeat fires (fail-open)")
	}
	if len(m.Scopes) == 0 {
		notes = append(notes, "no scopes: — ACCESS.md accepts any word for this system and none of them narrows anything")
	}
	scoped, described := 0, 0
	for _, a := range m.Actions {
		if a.Scope != "" {
			scoped++
		}
		if a.Doc != "" {
			described++
		}
	}
	if len(m.Scopes) > 0 && scoped == 0 {
		notes = append(notes, "scopes are declared but no action names one — the vocabulary exists and narrows nothing")
	}
	if scoped > 0 && described == 0 {
		notes = append(notes, "actions carry scopes but no doc: lines — free text cannot be narrowed, so every agent still gets the whole prompt doc")
	}
	return notes
}

// kindOf decides whether a file is an MCP configuration or a manifest. An MCP
// config names an endpoint and has no actions; a manifest is the other way
// round. Deciding by shape rather than by a flag keeps the file the single
// source of truth about what it is.
func kindOf(raw []byte) string {
	var probe struct {
		URL     string          `json:"url"`
		Actions json.RawMessage `json:"actions"`
	}
	if json.Unmarshal(raw, &probe) == nil && probe.URL != "" && len(probe.Actions) == 0 {
		return "mcp"
	}
	return "custom"
}

func category(c string) string {
	if c == "" {
		return target.CategoryOther
	}
	return c
}
