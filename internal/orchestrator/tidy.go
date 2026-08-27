package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Ein Home wächst nur. Nichts hat einen Agenten je gebeten, seinen eigenen
// Schreibtisch aufzuräumen, und nichts hat ihm gezeigt, was die Unordnung
// kostet.
//
// Gemessen an einem Home: 19,1 GB, davon 18,8 GB, die kein anderer teilt —
// darin zwei selbst installierte JDKs, ein Flutter, ein von Hand entpackter
// Datenbankserver, ein nachgebauter apt-Baum und Kratzverzeichnisse aus
// Tickets, die seit Wochen zu sind. Der Preis steht seit heute in den Phasen:
// 34 Sekunden Prüfen bei JEDEM Weckruf, 140 Sekunden und 426 MB Zurückschreiben
// nach JEDEM Lauf.
//
// Warum als Aufgabe und nicht als Kehrmaschine der Plattform: `239-fix-backup`
// ist eine Kopie aus einem Ticket vom August, und der Einzige, der das weiß,
// ist der Agent. Ein automatischer Kehrbesen über Dinge, die niemand verstanden
// hat, ist die Art, wie Gedächtnis verlorengeht — das Home IST das Gedächtnis
// (spec/16). Die Plattform misst, benennt und bittet; entscheiden tut der, der
// die Ablage angelegt hat.

// tidyTitle ist zugleich der Schlüssel der Entdopplung: solange eine offene
// Aufgabe mit diesem Titel steht, kommt keine zweite.
const tidyTitle = "Arbeitsplatz aufräumen"

// tidyEvery: wie oft überhaupt nachgesehen wird. Täglich reicht — ein Home
// wächst über Wochen, nicht über Stunden.
const tidyEvery = 24 * time.Hour

// homeJanitorLoop bittet Agenten, deren Home über die Schwelle gewachsen ist,
// um eine Aufräum-Runde.
func (o *Orchestrator) homeJanitorLoop(ctx context.Context) {
	if o.TidyHomeAbove <= 0 {
		o.Log.Info("home housekeeping is switched off (COVEY_HOME_TIDY_ABOVE_GB=0)")
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

// askForTidying legt für jeden Agenten, dessen letzter Schnappschuss über der
// Schwelle liegt, eine Aufgabe an — mit den Zahlen, an denen sich das Aufräumen
// lohnt. Ohne sie räumte er auf, was leicht zu finden ist, statt was groß ist.
// AskForTidying ist der Durchgang selbst — öffentlich, damit ein Test ihn
// anstoßen kann, ohne einen Tag zu warten.
func (o *Orchestrator) AskForTidying(ctx context.Context) {
	rows, err := o.Pool.Query(ctx, `
		SELECT DISTINCT ON (s.agent_id) s.agent_id, a.org_id, s.total_size, s.bytes_up, s.duration_ms
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
	type kandidat struct {
		agentID, orgID uuid.UUID
		total, bytesUp int64
		dauerMS        int
	}
	var kandidaten []kandidat
	for rows.Next() {
		var k kandidat
		if rows.Scan(&k.agentID, &k.orgID, &k.total, &k.bytesUp, &k.dauerMS) == nil {
			kandidaten = append(kandidaten, k)
		}
	}
	rows.Close()

	for _, k := range kandidaten {
		if k.total < o.TidyHomeAbove {
			continue
		}
		body := tidyBody(k.total, k.bytesUp, k.dauerMS)
		if _, err := o.Backlog.Create(ctx, k.orgID, k.agentID, tidyTitle, body, "housekeeping", 5); err != nil {
			o.Log.Warn("housekeeping task not created", "agent", k.agentID, "err", err)
			continue
		}
		o.Log.Info("housekeeping asked for", "agent", k.agentID, "home_bytes", k.total)
	}
}

// tidyBody ist der Auftrag. Er trägt die Zahlen, weil ohne sie niemand weiß,
// ob sich das lohnt — und die Grenzen, weil ein Home das Gedächtnis eines
// Agenten ist und kein Zwischenspeicher.
func tidyBody(total, bytesUp int64, dauerMS int) string {
	var b strings.Builder
	b.WriteString("Dein Arbeitsplatz ist gewachsen, und das kostet dich jeden Lauf Zeit.\n\n")
	fmt.Fprintf(&b, "- Home insgesamt: %s\n", gb(total))
	if bytesUp > 0 {
		fmt.Fprintf(&b, "- zuletzt zurückgeschrieben: %s in %s\n", gb(bytesUp), dauer(dauerMS))
	}
	b.WriteString(`
Sieh nach, was groß ist (du -sh ~/* | sort -h) und räum auf, was du benennen
kannst:

- Kratzverzeichnisse aus abgeschlossenen Tickets,
- ausgepackte Archive, die du nicht mehr brauchst,
- Werkzeuge, die dein Arbeitsplatz ohnehin mitbringt (siehe „Your workplace"
  in deinen Anweisungen — ein zweites JDK im Home hilft niemandem).

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
