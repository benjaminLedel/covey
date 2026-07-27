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

// Die Home-Arbeitskopie des Wikis (spec/05, hybride Speicherung): Die Control
// Plane ist die Quelle der Wahrheit, aber die Seiten werden zu Aufgabenbeginn
// als echte Markdown-Dateien unter ~/wiki/ materialisiert, damit der Agent sie
// mit normalen Datei-Tools (Read/Grep/Edit) lesen und bearbeiten kann. Direkt
// bearbeitete oder neu angelegte Seiten fließen bei Aufgabenende zurück.

// wikiPage ist die Daemon-lokale Sicht auf eine Wiki-Seite (das Paket daemon
// kennt den Control-Plane-Store nicht — Schichtentrennung).
type wikiPage struct {
	Slug  string   `json:"slug"`
	Title string   `json:"title"`
	Body  string   `json:"body"`
	Links []string `json:"links"`
}

const wikiReadme = `Dies ist dein Wiki — dein dauerhaftes Gedächtnis (spec/05).
Jede .md-Datei ist eine Seite; verweise mit [[slug]] auf andere Seiten.
Du kannst hier direkt lesen und schreiben; Änderungen werden bei Aufgabenende
in die Control Plane übernommen. Alternativ die Tools covey/wiki_read|write.
`

func wikiHash(title, body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:8])
}

// writeWikiFiles materialisiert die Seiten als ~/wiki/<slug>.md (mit Titel-
// Frontmatter) samt index.md und legt das Verzeichnis auch bei null Seiten an,
// damit der Agent dort neue Seiten anlegen kann. Rückgabe: slug→Hash-Snapshot
// zur späteren Änderungserkennung.
func writeWikiFiles(dir string, pages []wikiPage) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	snap := map[string]string{}
	for _, p := range pages {
		if p.Slug == "" {
			continue
		}
		content := "---\ntitle: " + p.Title + "\n---\n" + strings.TrimRight(p.Body, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, p.Slug+".md"), []byte(content), 0o600); err != nil {
			return nil, err
		}
		snap[p.Slug] = wikiHash(p.Title, p.Body)
	}
	// index.md + README (sind selbst keine Seiten und werden nie zurückgesynct).
	sorted := append([]wikiPage(nil), pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	var idx strings.Builder
	idx.WriteString("# Wiki-Index\n\n")
	if len(sorted) == 0 {
		idx.WriteString("_(noch leer)_\n")
	}
	for _, p := range sorted {
		idx.WriteString("- [[" + p.Slug + "]] — " + p.Title + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "index.md"), []byte(idx.String()), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte(wikiReadme), 0o600); err != nil {
		return nil, err
	}
	return snap, nil
}

// parseWikiFile trennt Titel-Frontmatter vom Body.
func parseWikiFile(content string) (title, body string) {
	if strings.HasPrefix(content, "---\n") {
		if end := strings.Index(content[4:], "\n---"); end >= 0 {
			fm := content[4 : 4+end]
			body = content[4+end+4:]
			body = strings.TrimPrefix(body, "\n")
			for _, line := range strings.Split(fm, "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(line), "title:"); ok {
					title = strings.TrimSpace(v)
				}
			}
			return title, body
		}
	}
	return "", content
}

// readWikiEdits liest die Home-Kopie und liefert die Seiten, die der Agent
// gegenüber dem Snapshot geändert oder neu angelegt hat (index.md/README.txt
// ausgenommen). Löschungen werden bewusst nicht zurückgespielt (fail-safe).
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
		title, body := parseWikiFile(string(raw))
		if strings.TrimSpace(body) == "" {
			continue
		}
		if snap[slug] == wikiHash(title, body) {
			continue // unverändert
		}
		out = append(out, wikiPage{Slug: slug, Title: title, Body: body})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// materializeWiki holt alle Seiten von der Control Plane und schreibt die
// Home-Arbeitskopie. Best-effort: schlägt der Abruf fehl, wird nicht angefasst
// (nil-Snapshot → kein Zurücksynchronisieren, um nichts zu überschreiben).
func (c *Client) materializeWiki(ctx context.Context) map[string]string {
	if c.homeDir == "" {
		return nil
	}
	resp, err := c.wiki(ctx, RequestWiki{Op: "list"})
	if err != nil || !resp.OK {
		return nil
	}
	var pages []wikiPage
	if len(resp.Data) > 0 {
		_ = json.Unmarshal(resp.Data, &pages)
	}
	snap, err := writeWikiFiles(filepath.Join(c.homeDir, "wiki"), pages)
	if err != nil {
		c.log.Warn("wiki-arbeitskopie konnte nicht geschrieben werden", "err", err)
		return nil
	}
	return snap
}

// syncWikiBack spielt vom Agenten geänderte/neu angelegte Seiten in die Control
// Plane zurück (spec/05). Best-effort.
func (c *Client) syncWikiBack(ctx context.Context, snap map[string]string) {
	if c.homeDir == "" || snap == nil {
		return
	}
	edits, err := readWikiEdits(filepath.Join(c.homeDir, "wiki"), snap)
	if err != nil {
		c.log.Warn("wiki-arbeitskopie konnte nicht gelesen werden", "err", err)
		return
	}
	for _, p := range edits {
		if _, err := c.wiki(ctx, RequestWiki{Op: "write", Slug: p.Slug, Title: p.Title, Body: p.Body}); err != nil {
			c.log.Warn("wiki-seite konnte nicht zurückgesynct werden", "slug", p.Slug, "err", err)
		}
	}
	if len(edits) > 0 {
		c.log.Info(fmt.Sprintf("wiki: %d Seite(n) aus der Home-Kopie übernommen", len(edits)))
	}
}
