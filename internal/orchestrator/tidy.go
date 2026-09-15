package orchestrator

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
)

// A home only grows. Nothing ever asked an agent to tidy its own desk, and
// nothing showed it what the mess costs.
//
// Measured on one home: 19.1 GB, 18.8 GB of it shared with nobody — two
// self-installed JDKs, a Flutter, a database server unpacked by hand, a rebuilt
// apt tree and scratch directories from tickets closed for weeks. The price is
// in the phases: 34 seconds of checking at EVERY wake, 140 seconds and 426 MB
// of writing back after EVERY run.
//
// The other kind of mess costs no gigabytes. One developer home held 806
// entries directly in its root — `shot-*`, `suite-*`, `harness-*` by the dozen
// (#272). A person opening the file browser finds nothing there, and neither
// does the agent in its next run. The size never pointed at it, so the count
// does.
//
// Why a task and not a sweep by the platform: `239-fix-backup` is a copy from a
// ticket in August, and the only one who knows that is the agent. An automatic
// broom over things nobody has understood is how memory gets lost — the home IS
// the memory (spec/16). The platform measures, names and asks; the one who
// filed it decides. The one exception is ~/scratch, where the agent was told
// beforehand what it is for (internal/daemon/scratch.go).

// tidyTitle is also the deduplication key: while an open task with this title
// stands, no second one comes.
const tidyTitle = "Arbeitsplatz aufräumen"

// tidyEvery: how often anyone looks at all. Daily is enough — a home grows over
// weeks, not hours.
const tidyEvery = 24 * time.Hour

// tidyPatterns is how many name patterns the task lists. Five cover the heap on
// the measured home; a longer list is read as far as the fifth line anyway.
const tidyPatterns = 5

// homeJanitorLoop asks agents whose home has grown past a threshold for a
// round of tidying.
func (o *Orchestrator) homeJanitorLoop(ctx context.Context) {
	if o.TidyHomeAbove <= 0 && !o.countsEntries() {
		o.Log.Info("home housekeeping is switched off (COVEY_HOME_TIDY_ABOVE_GB=0, COVEY_HOME_TIDY_ABOVE_ENTRIES=0)")
		return
	}
	t := time.NewTicker(tidyEvery)
	defer t.Stop()
	o.AskForTidying(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.AskForTidying(ctx)
		}
	}
}

// countsEntries: the count needs the manifest, and the manifest needs the
// store. Without one (COVEY_HOME_STORE=false) there is only the size.
func (o *Orchestrator) countsEntries() bool {
	return o.TidyEntriesAbove > 0 && o.Blobs != nil
}

// tidyMeasure is what one home was measured at, and which of the two
// thresholds it crossed.
type tidyMeasure struct {
	total, bytesUp int64
	durationMS     int
	// root holds the names directly in the home; nil when they were not
	// counted (no store, manifest unreadable).
	root             []string
	large, scattered bool
}

// AskForTidying creates a task for every agent whose newest snapshot lies above
// a threshold — with the figures that make tidying worth it. Without them the
// agent would tidy what is easy to find rather than what matters. Public so a
// test can trigger the pass without waiting a day.
func (o *Orchestrator) AskForTidying(ctx context.Context) {
	rows, err := o.Pool.Query(ctx, `
		SELECT DISTINCT ON (s.agent_id) s.agent_id, a.org_id, s.manifest_hash,
		       s.total_size, s.bytes_up, s.duration_ms
		FROM home_snapshots s
		JOIN agents a ON a.id = s.agent_id
		JOIN organizations org ON org.id = a.org_id
		WHERE NOT a.killed AND NOT org.fleet_killed AND a.hired_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM backlog_tasks t
		      WHERE t.agent_id = s.agent_id AND t.title = $1
		        AND t.state NOT IN ('done','failed','cancelled'))
		ORDER BY s.agent_id, s.created_at DESC`, tidyTitle)
	if err != nil {
		o.Log.Warn("home housekeeping query", "err", err)
		return
	}
	type candidate struct {
		agentID, orgID uuid.UUID
		manifest       string
		total, bytesUp int64
		durationMS     int
	}
	var candidates []candidate
	for rows.Next() {
		var c candidate
		if rows.Scan(&c.agentID, &c.orgID, &c.manifest, &c.total, &c.bytesUp, &c.durationMS) == nil {
			candidates = append(candidates, c)
		}
	}
	rows.Close()

	for _, c := range candidates {
		m := tidyMeasure{total: c.total, bytesUp: c.bytesUp, durationMS: c.durationMS}
		m.large = o.TidyHomeAbove > 0 && c.total >= o.TidyHomeAbove
		if o.countsEntries() {
			// One manifest per agent per day, and only for agents without an open
			// task. A large home's manifest is large too (hundreds of thousands
			// of entries); that is the price of counting from what the store
			// already holds instead of asking a runner that may be offline.
			man, err := homestore.Load(ctx, o.Blobs, c.orgID, c.manifest)
			if err != nil {
				o.Log.Warn("home housekeeping: manifest not readable", "agent", c.agentID, "err", err)
			} else {
				m.root = rootEntries(man)
				m.scattered = len(m.root) > o.TidyEntriesAbove
			}
		}
		if !m.large && !m.scattered {
			continue
		}
		if _, err := o.Backlog.Create(ctx, c.orgID, c.agentID, tidyTitle, tidyBody(m), "housekeeping", 5); err != nil {
			o.Log.Warn("housekeeping task not created", "agent", c.agentID, "err", err)
			continue
		}
		o.Log.Info("housekeeping asked for", "agent", c.agentID, "home_bytes", c.total, "root_entries", len(m.root))
	}
}

// rootEntries names what lies directly in the home. Paths excluded from the
// sync (COVEY_HOME_EXCLUDES) are not in the manifest and so not counted — they
// are the scrap class nobody has to tidy.
func rootEntries(m homestore.Manifest) []string {
	var out []string
	for _, e := range m.Entries {
		if e.Path != "" && !strings.Contains(e.Path, "/") {
			out = append(out, e.Path)
		}
	}
	return out
}

// namePattern is one kind of leftover: the prefix its names share, how many
// there are, and one of them as an example.
type namePattern struct {
	Prefix  string
	Count   int
	Example string
}

// patternPrefix is a name up to its first separator or digit. A ticket or run
// number is what makes one kind of leftover look like forty different ones:
// shot-3.png, shot-41.png, suite-888.log.
var patternPrefix = regexp.MustCompile(`^[^-_. 0-9]+`)

// namePatterns groups names by prefix and returns the largest groups of at
// least three. Hidden entries are left out: `.cache` and `.npm` belong to tools,
// not to tickets.
func namePatterns(names []string, limit int) []namePattern {
	by := map[string]*namePattern{}
	for _, n := range names {
		if strings.HasPrefix(n, ".") {
			continue
		}
		p := patternPrefix.FindString(n)
		if p == "" {
			continue
		}
		g := by[p]
		if g == nil {
			g = &namePattern{Prefix: p, Example: n}
			by[p] = g
		}
		g.Count++
		if n < g.Example {
			g.Example = n
		}
	}
	var out []namePattern
	for _, g := range by {
		if g.Count >= 3 {
			out = append(out, *g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Prefix < out[j].Prefix
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// tidyBody is the assignment. It carries the figures, because without them
// nobody knows whether it is worth it — and the limits, because a home is an
// agent's memory and not a cache.
func tidyBody(m tidyMeasure) string {
	var b strings.Builder
	switch {
	case m.large && m.scattered:
		b.WriteString("Dein Arbeitsplatz ist gewachsen und unübersichtlich geworden, und beides kostet dich Zeit.\n\n")
	case m.scattered:
		b.WriteString("Dein Arbeitsplatz ist unübersichtlich geworden: Direkt in deinem Home liegt so viel, " +
			"dass dort niemand mehr etwas findet — du selbst beim nächsten Lauf eingeschlossen.\n\n")
	default:
		b.WriteString("Dein Arbeitsplatz ist gewachsen, und das kostet dich jeden Lauf Zeit.\n\n")
	}
	fmt.Fprintf(&b, "- Home insgesamt: %s\n", gb(m.total))
	if m.bytesUp > 0 {
		fmt.Fprintf(&b, "- zuletzt zurückgeschrieben: %s in %s\n", gb(m.bytesUp), dauer(m.durationMS))
	}
	if m.root != nil {
		fmt.Fprintf(&b, "- Einträge direkt in ~: %d\n", len(m.root))
	}
	if m.large {
		b.WriteString(`
Sieh nach, was groß ist (du -sh ~/* | sort -h) und räum auf, was du benennen
kannst:

- Kratzverzeichnisse aus abgeschlossenen Tickets,
- ausgepackte Archive, die du nicht mehr brauchst,
- Werkzeuge, die dein Arbeitsplatz ohnehin mitbringt (siehe „Your workplace"
  in deinen Anweisungen — ein zweites JDK im Home hilft niemandem).
`)
	}
	if m.scattered {
		if patterns := namePatterns(m.root, tidyPatterns); len(patterns) > 0 {
			b.WriteString("\nDie häufigsten Namen direkt in ~:\n\n")
			for _, p := range patterns {
				fmt.Fprintf(&b, "- `%s*`: %d (z. B. `%s`)\n", p.Prefix, p.Count, p.Example)
			}
		}
		b.WriteString(`
Eine Nummer im Namen ist meist ein Ticket oder ein Lauf. Ist der Vorgang
abgeschlossen, hat die Datei ihren Zweck erfüllt. Was du weiter brauchst,
bekommt EIN Verzeichnis mit einem Namen, der sagt, was darin liegt — nicht
vierzig Dateien nebeneinander. Zwischenstände eines Laufs gehören nach
~/scratch/<Aufgaben-ID>/: dort startet jeder Lauf, und das Verzeichnis
verschwindet eine Woche nach dem letzten.
`)
	}
	b.WriteString(`
Was du NICHT anfasst: alles, wovon du nicht sagen kannst, wofür es da war.
Dein Home ist dein Gedächtnis, nicht dein Zwischenspeicher. Im Zweifel stehen
lassen und im Ergebnis erwähnen.

Berichte am Ende, was du entfernt hast und wie viel es gebracht hat.`)
	return b.String()
}

func gb(n int64) string {
	if n < 1<<30 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%.1f GB", float64(n)/float64(1<<30))
}

func dauer(ms int) string {
	if ms < 60_000 {
		return fmt.Sprintf("%d s", ms/1000)
	}
	return fmt.Sprintf("%d min", ms/60_000)
}
