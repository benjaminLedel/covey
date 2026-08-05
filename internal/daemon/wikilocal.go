package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The home working copy of the wiki (spec/05, hybrid storage): the control
// plane is the source of truth, but the pages are materialized as real Markdown
// files under ~/wiki/ at the start of a task, so the agent can read and edit
// them with ordinary file tools (Read/Grep/Edit). Pages edited directly or
// newly created flow back when the task ends.

// wikiPage is the daemon-local view of a wiki page (the daemon package does not
// know the control plane's store — layer separation).
type wikiPage struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Links []string `json:"links"`
	Type  string   `json:"type"`
	Tags  []string `json:"tags"`
}

const wikiReadme = `This is your wiki — your durable memory (spec/05).

Every .md file is a page about EXACTLY ONE thing: a customer, a project, a
colleague, a system, a recurring problem. Not a diary — whatever belongs to a
single task only is a note, not a page.

The head of every page:

    ---
    title: Customer ACME
    type: kunde
    tags: billing, escalation
    ---

type is one of: kunde, projekt, system, person, problem, thema.
Refer to other pages with [[slug]] — the linking IS the memory.

You can read and write here directly; changes are taken over into the control
plane when the task ends. Alternatively use the tools
covey/wiki_read|write|append.
`

// removeLocalWikiFile deletes a page's working copy in the home after
// covey/wiki_delete has removed it in the control plane. Without it the file
// stays for the rest of the run and the agent finds its own deleted page again
// with Grep/Read — which is exactly what one QA agent noted as "wiki_search
// still returns hits from a stale index".
//
// The slug comes from the AGENT, and that makes this the one place in this file
// where the path is not the control plane's own (everywhere else the slug comes
// out of a page list and has been through slugify). It is therefore contained
// twice, for the same reason internal/sandboxfs is:
//
//   - The name is checked: a plain file name, no path separator, no leading dot.
//     That is the early, comprehensible refusal.
//   - The deletion goes through os.Root, so the OPERATING SYSTEM enforces that
//     it stays inside the wiki directory.
//
// The second one is not closing a hole the first one leaves open — with a
// single-segment name and os.Remove, which does not follow the final symlink,
// the textual check already holds. It is here because containment should not
// depend on that argument being made correctly every time: whoever later
// relaxes the name check (a slug with a subdirectory, say) inherits a safe
// deletion instead of a hole, and it is the same decision internal/sandboxfs
// took for the whole workplace.
//
// Best effort: the deletion in the control plane has happened, that is what
// counts. A file that will not go away is corrected by the next
// materialisation (writeWikiFiles prunes orphans).
func (p *actionProxy) removeLocalWikiFile(slug string) {
	home := p.client.homeDir
	slug = strings.TrimSpace(slug)
	if home == "" || slug == "" || slug != filepath.Base(slug) || strings.HasPrefix(slug, ".") {
		return
	}
	root, err := os.OpenRoot(filepath.Join(home, "wiki"))
	if err != nil {
		return
	}
	defer root.Close()
	_ = root.Remove(slug + ".md")
}

func wikiHash(title, body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:8])
}

// writeWikiFiles materializes the pages as ~/wiki/<slug>.md (with title
// frontmatter) plus index.md, and creates the directory even when there are zero
// pages so the agent can add new ones there. Returns: slug→hash snapshot for
// detecting changes later.
//
// Pages that no longer exist in the control plane are removed from the home in
// the process: the control plane is the source of truth, and a leftover file
// would resurrect a deleted or merged page at the next sync-back.
func writeWikiFiles(dir string, pages []wikiPage) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	snap := map[string]string{}
	for _, p := range pages {
		if p.Slug == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, p.Slug+".md"), []byte(renderWikiFile(p)), 0o600); err != nil {
			return nil, err
		}
		snap[p.Slug] = wikiHash(p.Title, p.Body)
	}
	pruneWikiOrphans(dir, snap)
	// index.md + README (they are not pages themselves and are never synced
	// back). Grouped by type — that way the agent sees at a glance which
	// entities it already keeps, instead of just an alphabetical heap.
	sorted := append([]wikiPage(nil), pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	var idx strings.Builder
	idx.WriteString("# Wiki index\n\n")
	if len(sorted) == 0 {
		idx.WriteString("_(still empty)_\n")
	}
	for _, group := range []string{"kunde", "projekt", "system", "person", "problem", "thema", ""} {
		var section []wikiPage
		for _, p := range sorted {
			if p.Type == group {
				section = append(section, p)
			}
		}
		if len(section) == 0 {
			continue
		}
		heading := group
		if heading == "" {
			heading = "no type — please categorize"
		}
		idx.WriteString("\n## " + heading + "\n\n")
		for _, p := range section {
			idx.WriteString("- [[" + p.Slug + "]] — " + p.Title + "\n")
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(idx.String()), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(wikiReadme), 0o600); err != nil {
		return nil, err
	}
	return snap, nil
}

// pruneWikiOrphans deletes home files for which the control plane has no page
// (any more). Best-effort: if a remove fails, the file stays — the sync-back
// additionally catches that case through its live comparison.
func pruneWikiOrphans(dir string, live map[string]string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "index.md" {
			continue
		}
		if _, ok := live[strings.TrimSuffix(name, ".md")]; !ok {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

// renderWikiFile writes a page as Markdown with frontmatter. Type and tags only
// appear if they exist — an empty `type:` line invites filling it with just
// anything.
func renderWikiFile(p wikiPage) string {
	var b strings.Builder
	b.WriteString("---\ntitle: ")
	b.WriteString(p.Title)
	b.WriteString("\n")
	if p.Type != "" {
		b.WriteString("type: " + p.Type + "\n")
	}
	if len(p.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(p.Tags, ", ") + "\n")
	}
	b.WriteString("---\n")
	b.WriteString(strings.TrimRight(p.Body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// parseWikiFile separates the frontmatter from the body. Read are title, type
// and tags — until now everything but the title fell by the wayside, which is
// why an agent could not categorize a page at all, even when it tried.
func parseWikiFile(content string) (p wikiPage) {
	if !strings.HasPrefix(content, "---\n") {
		return wikiPage{Body: content}
	}
	end := strings.Index(content[4:], "\n---")
	if end < 0 {
		return wikiPage{Body: content}
	}
	p.Body = strings.TrimPrefix(content[4+end+4:], "\n")
	for _, line := range strings.Split(content[4:4+end], "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "title:"):
			p.Title = strings.TrimSpace(strings.TrimPrefix(line, "title:"))
		case strings.HasPrefix(line, "type:"):
			p.Type = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "type:")))
		case strings.HasPrefix(line, "tags:"):
			for _, t := range strings.Split(strings.TrimPrefix(line, "tags:"), ",") {
				// Accept the YAML list form "- tag" just like "a, b".
				if t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), "[]-\"'")); t != "" {
					p.Tags = append(p.Tags, t)
				}
			}
		}
	}
	return p
}

// readWikiEdits reads the home copy and returns the pages the agent changed or
// newly created relative to the snapshot (index.md/README.txt excluded).
// Deletions are deliberately not played back (fail-safe).
func readWikiEdits(dir string, snap map[string]string) ([]wikiPage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []wikiPage
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || name == "index.md" {
			continue
		}
		slug := strings.TrimSuffix(name, ".md")
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		pg := parseWikiFile(string(raw))
		if strings.TrimSpace(pg.Body) == "" {
			continue
		}
		if snap[slug] == wikiHash(pg.Title, pg.Body) {
			continue // unchanged
		}
		pg.Slug = slug
		out = append(out, pg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// listWikiPages fetches the control plane's current page list.
func (c *Client) listWikiPages(ctx context.Context) ([]wikiPage, bool) {
	resp, err := c.wiki(ctx, RequestWiki{Op: "list"})
	if err != nil || !resp.OK {
		return nil, false
	}
	var pages []wikiPage
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &pages)
	}
	return pages, true
}

// materializeWiki fetches all pages from the control plane and writes the home
// working copy. Best-effort: if the fetch fails, nothing is touched (nil
// snapshot → no sync-back, so that nothing gets overwritten).
func (c *Client) materializeWiki(ctx context.Context) map[string]string {
	if c.homeDir == "" {
		return nil
	}
	pages, ok := c.listWikiPages(ctx)
	if !ok {
		return nil
	}
	snap, err := writeWikiFiles(filepath.Join(c.homeDir, "wiki"), pages)
	if err != nil {
		c.log.Warn("wiki working copy could not be written", "err", err)
		return nil
	}
	return snap
}

// partitionWikiEdits separates real changes from dead entries. A page that
// existed at the start of the task (it is in the snapshot) and is meanwhile
// missing in the control plane was deleted or merged during the run — its home
// file must NOT resurrect it, otherwise the sync-back would undo the agent's own
// cleanup within the same run (spec/05).
//
// Pages the snapshot does not know were created during the run and are written.
// If the live list is not retrievable (haveLive=false), the fail-safe path
// applies: better to write too much than to lose a new page.
func partitionWikiEdits(edits []wikiPage, snap map[string]string, live map[string]bool, haveLive bool) (writes []wikiPage, stale []string) {
	for _, p := range edits {
		_, known := snap[p.Slug]
		if haveLive && known && !live[p.Slug] {
			stale = append(stale, p.Slug)
			continue
		}
		writes = append(writes, p)
	}
	return writes, stale
}

// syncWikiBack plays pages changed/newly created by the agent back into the
// control plane (spec/05). Best-effort.
func (c *Client) syncWikiBack(ctx context.Context, snap map[string]string) {
	if c.homeDir == "" || snap == nil {
		return
	}
	dir := filepath.Join(c.homeDir, "wiki")
	edits, err := readWikiEdits(dir, snap)
	if err != nil {
		c.log.Warn("wiki working copy could not be read", "err", err)
		return
	}
	if len(edits) == 0 {
		return
	}
	live := map[string]bool{}
	pages, haveLive := c.listWikiPages(ctx)
	for _, p := range pages {
		live[p.Slug] = true
	}
	writes, stale := partitionWikiEdits(edits, snap, live, haveLive)
	for _, slug := range stale {
		_ = os.Remove(filepath.Join(dir, slug+".md"))
		c.log.Info("wiki: page deleted during the run was not written back", "slug", slug)
	}
	for _, p := range writes {
		if _, err := c.wiki(ctx, RequestWiki{Op: "write", Slug: p.Slug, Title: p.Title,
			Body: p.Body, Type: p.Type, Tags: p.Tags}); err != nil {
			c.log.Warn("wiki page could not be synced back", "slug", p.Slug, "err", err)
		}
	}
	if len(writes) > 0 {
		c.log.Info(fmt.Sprintf("wiki: took over %d page(s) from the home copy", len(writes)))
	}
}
