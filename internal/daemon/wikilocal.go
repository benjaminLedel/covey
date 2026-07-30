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
	Type  string   `json:"type"`
	Tags  []string `json:"tags"`
}

const wikiReadme = `Dies ist dein Wiki — dein dauerhaftes Gedächtnis (spec/05).

Jede .md-Datei ist eine Seite über GENAU EINE Sache: einen Kunden, ein Projekt,
einen Kollegen, ein System, ein wiederkehrendes Problem. Kein Tagebuch — was nur
zu einer einzelnen Aufgabe gehört, ist eine Notiz, keine Seite.

Kopf jeder Seite:

    ---
    title: Kunde ACME
    type: kunde
    tags: abrechnung, eskalation
    ---

type ist eines von: kunde, projekt, system, person, problem, thema.
Verweise mit [[slug]] auf andere Seiten — die Verlinkung IST das Gedächtnis.

Du kannst hier direkt lesen und schreiben; Änderungen werden bei Aufgabenende
in die Control Plane übernommen. Alternativ die Tools covey/wiki_read|write|append.
`

func wikiHash(title, body string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(title) + "\x00" + strings.TrimSpace(body)))
	return hex.EncodeToString(sum[:8])
}

// writeWikiFiles materialisiert die Seiten als ~/wiki/<slug>.md (mit Titel-
// Frontmatter) samt index.md und legt das Verzeichnis auch bei null Seiten an,
// damit der Agent dort neue Seiten anlegen kann. Rückgabe: slug→Hash-Snapshot
// zur späteren Änderungserkennung.
//
// Seiten, die es in der Control Plane nicht mehr gibt, werden dabei aus dem
// Home entfernt: die Control Plane ist die Quelle der Wahrheit, und eine
// liegengebliebene Datei würde eine gelöschte oder verschmolzene Seite beim
// nächsten Rücksync wieder auferstehen lassen.
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
	// index.md + README (sind selbst keine Seiten und werden nie zurückgesynct).
	// Nach Typ gruppiert — so sieht der Agent beim Nachschlagen sofort, welche
	// Entitäten er schon führt, statt nur eine alphabetische Halde.
	sorted := append([]wikiPage(nil), pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })
	var idx strings.Builder
	idx.WriteString("# Wiki-Index\n\n")
	if len(sorted) == 0 {
		idx.WriteString("_(noch leer)_\n")
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
			heading = "ohne Typ — bitte einordnen"
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

// pruneWikiOrphans löscht Home-Dateien, zu denen es in der Control Plane keine
// Seite (mehr) gibt. Best-effort: scheitert ein Remove, bleibt die Datei liegen
// — der Rücksync fängt den Fall über den Live-Abgleich zusätzlich ab.
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

// renderWikiFile schreibt eine Seite als Markdown mit Frontmatter. Typ und Tags
// stehen nur da, wenn es sie gibt — eine leere `type:`-Zeile lädt dazu ein, sie
// mit irgendetwas zu füllen.
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

// parseWikiFile trennt das Frontmatter vom Body. Gelesen werden title, type und
// tags — bis hierher fiel alles außer title unter den Tisch, weshalb ein Agent
// eine Seite gar nicht einordnen konnte, selbst wenn er es versuchte.
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
				// YAML-Listenform "- tag" genauso annehmen wie "a, b".
				if t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), "[]-\"'")); t != "" {
					p.Tags = append(p.Tags, t)
				}
			}
		}
	}
	return p
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
		pg := parseWikiFile(string(raw))
		if strings.TrimSpace(pg.Body) == "" {
			continue
		}
		if snap[slug] == wikiHash(pg.Title, pg.Body) {
			continue // unverändert
		}
		pg.Slug = slug
		out = append(out, pg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

// listWikiPages holt die aktuelle Seitenliste der Control Plane.
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

// materializeWiki holt alle Seiten von der Control Plane und schreibt die
// Home-Arbeitskopie. Best-effort: schlägt der Abruf fehl, wird nicht angefasst
// (nil-Snapshot → kein Zurücksynchronisieren, um nichts zu überschreiben).
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
		c.log.Warn("wiki-arbeitskopie konnte nicht geschrieben werden", "err", err)
		return nil
	}
	return snap
}

// partitionWikiEdits trennt echte Änderungen von Karteileichen. Eine Seite, die
// zu Aufgabenbeginn existierte (steht im Snapshot) und in der Control Plane
// inzwischen fehlt, wurde während des Laufs gelöscht oder verschmolzen — ihre
// Home-Datei darf sie NICHT wieder auferstehen lassen, sonst macht der Rücksync
// die Aufräumarbeit des Agenten im selben Lauf zunichte (spec/05).
//
// Seiten, die der Snapshot nicht kennt, sind im Lauf neu entstanden und werden
// geschrieben. Ist die Live-Liste nicht abrufbar (haveLive=false), gilt der
// fail-safe Weg: lieber zu viel schreiben als eine neue Seite verlieren.
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

// syncWikiBack spielt vom Agenten geänderte/neu angelegte Seiten in die Control
// Plane zurück (spec/05). Best-effort.
func (c *Client) syncWikiBack(ctx context.Context, snap map[string]string) {
	if c.homeDir == "" || snap == nil {
		return
	}
	dir := filepath.Join(c.homeDir, "wiki")
	edits, err := readWikiEdits(dir, snap)
	if err != nil {
		c.log.Warn("wiki-arbeitskopie konnte nicht gelesen werden", "err", err)
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
		c.log.Info("wiki: im Lauf gelöschte Seite nicht zurückgeschrieben", "slug", slug)
	}
	for _, p := range writes {
		if _, err := c.wiki(ctx, RequestWiki{Op: "write", Slug: p.Slug, Title: p.Title,
			Body: p.Body, Type: p.Type, Tags: p.Tags}); err != nil {
			c.log.Warn("wiki-seite konnte nicht zurückgesynct werden", "slug", p.Slug, "err", err)
		}
	}
	if len(writes) > 0 {
		c.log.Info(fmt.Sprintf("wiki: %d Seite(n) aus der Home-Kopie übernommen", len(writes)))
	}
}
