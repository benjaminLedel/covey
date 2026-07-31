package agents

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Der Config-Lint prüft Agenten-Konfigurationen gegen Muster, die in der Praxis
// Endlosschleifen, verbrannte Budgets oder unbrauchbare Boards erzeugt haben.
//
// Warum als eigener Prüfschritt und nicht als Verbot beim Speichern: Es sind
// **Warnungen mit Kontext**, keine harten Fehler. Ein 2-Minuten-Heartbeat ist
// für einen Agenten, der nur ein Postfach sichtet, völlig in Ordnung und für
// einen, der Repos klont, ruinös — die Regel kann das unterscheiden, aber nie
// mit letzter Sicherheit. Wer es besser weiß, soll speichern dürfen.
//
// Die Prüfung ist eine reine Funktion über Subject: Der Aufrufer sammelt die
// Fakten (CLI aus der Datenbank), die Regeln entscheiden ohne I/O. So sind sie
// testbar und später von UI oder API wiederverwendbar.

// Severity eines Befunds.
const (
	SeverityWarn = "warn" // sollte geändert werden
	SeverityInfo = "info" // Hinweis, oft in Ordnung
)

// Finding ist ein einzelner Lint-Befund an einem Agenten.
type Finding struct {
	AgentSlug string `json:"agent_slug"`
	Rule      string `json:"rule"`
	Severity  string `json:"severity"`
	// File/Line zeigen auf die Fundstelle in der Config. Leer bzw. 0 bei
	// Befunden, die aus dem Laufzeit-Zustand kommen (Spalten, Abbrüche).
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// Subject bündelt alles, was die Regeln über einen Agenten wissen müssen.
// Die Laufzeit-Felder sind optional: Fehlen sie (Wert 0/nil), entfallen die
// Regeln, die sie brauchen — der Lint läuft dann nur über die Config.
type Subject struct {
	Slug  string
	Files map[string]string

	// Skills sind die Fähigkeiten des Agenten (internal/skills): Skill-Name →
	// Inhalt seiner Dateien. Sie gehören in die Prüfung, weil Prozeduren aus
	// PLAYBOOKS.md dorthin wandern — ein Kommentar-Schritt in einem Skill ist
	// derselbe Schritt. Sähe der Lint ihn nicht, warnte er bei jedem
	// umgestellten Agenten falsch, und eine Regel, die bei guten Configs
	// meckert, wird ignoriert.
	Skills map[string]string

	// AgentStages sind die Board-Spalten, die der Agent selbst angelegt hat
	// (created_by='agent').
	AgentStages []string
	// TurnLimitFailures zählt Aufgaben, die am Turn-Limit abgebrochen sind.
	TurnLimitFailures int
	// MaxTurns ist das Turn-Limit des Agenten (0 = Runtime-Default).
	MaxTurns int
}

// heavySystems sind Zielsysteme, deren Nutzung einen Lauf typischerweise in den
// Minutenbereich hebt: Repos klonen, Builds, echte Browser-Sitzungen. Für sie
// ist ein enger Heartbeat-Takt teuer und selten sinnvoll.
var heavySystems = map[string]bool{"gitlab": true, "dev": true, "browser": true}

// commentActions sind die Aktionen, mit denen ein Agent eine sichtbare Spur im
// Zielsystem hinterlässt. Ohne eine davon kippt die `nur-wenn:`-Flanke nie.
var commentActions = []string{"comment", "comment_mr", "comment_external", "reply", "create_issue"}

// stageWithItemID erkennt Spaltennamen, die einen konkreten Vorgang benennen
// statt eines Arbeitszustands ("#83 CSV-Import", "MR !1641").
var stageWithItemID = regexp.MustCompile(`[#!]\d+`)

// maxAgentStages ist die Zahl selbst angelegter Spalten, ab der ein Board keine
// Zustände mehr zeigt, sondern einen Verlauf.
const maxAgentStages = 8

// Lint prüft einen Agenten und liefert die Befunde, schwerste zuerst.
func Lint(s Subject) []Finding {
	var out []Finding
	systems := lintSystems(s.Files["ACCESS.md"])
	hbs, _ := ParseHeartbeat(s.Files["HEARTBEAT.md"])

	out = append(out, lintIntervals(s, hbs, systems)...)
	out = append(out, lintVisibleTrace(s, hbs)...)
	out = append(out, lintBlockedOnPolling(s, systems)...)
	out = append(out, lintStages(s)...)
	out = append(out, lintTurnLimit(s)...)

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Severity == SeverityWarn && out[j].Severity != SeverityWarn
	})
	return out
}

func lintSystems(access string) map[string]bool {
	systems := map[string]bool{}
	for _, a := range ParseAccess(access) {
		systems[strings.ToLower(a.System)] = true
	}
	return systems
}

func (s Subject) hasHeavySystem(systems map[string]bool) bool {
	for name := range systems {
		if heavySystems[name] {
			return true
		}
	}
	return false
}

// lintIntervals: Das Intervall muss zur Dauer eines Laufs passen, nicht zur
// gewünschten Reaktionszeit. Ein Agent, der ein Repo klont und Tests laufen
// lässt, braucht Minuten bis Viertelstunden — bei `alle: 2m` beginnt der
// nächste Lauf, bevor der vorige verstanden hat, worum es geht.
func lintIntervals(s Subject, hbs []Heartbeat, systems map[string]bool) []Finding {
	heavy := s.hasHeavySystem(systems)
	var out []Finding
	for _, hb := range hbs {
		if hb.Every <= 0 {
			continue // Tageszeit-Form, kein Intervall
		}
		mins := hb.Every.Minutes()
		switch {
		case heavy && mins < 5:
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "heartbeat-intervall-zu-kurz", Severity: SeverityWarn,
				File: "HEARTBEAT.md", Line: heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name),
				Message: fmt.Sprintf("%q feuert alle %s, der Agent nutzt aber ein Zielsystem mit langen Läufen (%s)",
					hb.Name, shortDur(hb.Every), joinHeavy(systems)),
				Hint: "Läufe mit Checkout/Build/Browser dauern Minuten bis Viertelstunden — mindestens 5m, realistisch 15m.",
			})
		case !heavy && mins < 2:
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "heartbeat-intervall-zu-kurz", Severity: SeverityWarn,
				File: "HEARTBEAT.md", Line: heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name),
				Message: fmt.Sprintf("%q feuert alle %s", hb.Name, shortDur(hb.Every)),
				Hint:    "Unter 2 Minuten lohnt kaum ein Lauf — jeder Wake kostet einen Runtime-Start.",
			})
		}
	}
	return out
}

// lintVisibleTrace: Die `nur-wenn:`-Bedingung von GitLab triggert auf die
// Flanke — ein Vorgang gilt als erledigt, sobald der letzte Nicht-System-
// Kommentar vom Bot stammt. Ein Playbook, das arbeitet ohne zu kommentieren,
// hinterlässt keine Flanke: derselbe Vorgang weckt den Agenten in jedem
// Intervall erneut. Das ist die Ursache der teuersten Schleife im System.
func lintVisibleTrace(s Subject, hbs []Heartbeat) []Finding {
	gated := false
	var line int
	for _, hb := range hbs {
		if strings.HasPrefix(strings.ToLower(hb.OnlyIf), "gitlab") {
			gated, line = true, heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name)
			break
		}
	}
	if !gated {
		return nil
	}
	haystack := strings.ToLower(s.Files["PLAYBOOKS.md"] + "\n" + s.Files["HEARTBEAT.md"] + "\n" +
		s.Files["SOUL.md"] + "\n" + s.skillText())
	for _, a := range commentActions {
		if strings.Contains(haystack, a) {
			return nil
		}
	}
	return []Finding{{
		AgentSlug: s.Slug, Rule: "keine-sichtbare-spur", Severity: SeverityWarn,
		File: "PLAYBOOKS.md", Line: line,
		Message: "Der Heartbeat ist über gitlab gegatet, aber kein Playbook-Schritt hinterlässt einen Kommentar",
		Hint:    "Ein stiller Lauf hinterlässt keine Flanke — der Vorgang gilt beim nächsten Intervall wieder als unbearbeitet und weckt erneut. Wer arbeitet, kommentiert.",
	}}
}

// lintBlockedOnPolling: GitLab und E-Mail haben keinen Webhook-Eingang, der eine
// geparkte Aufgabe wieder aufweckt. Ein Agent, der dort auf `blocked` geht,
// wartet auf ein Ereignis, das nie ankommt.
func lintBlockedOnPolling(s Subject, systems map[string]bool) []Finding {
	if !systems["gitlab"] && !systems["email"] {
		return nil
	}
	// Quelle für Quelle statt über einen zusammengeklebten Text: Sonst zeigt der
	// Befund auf eine Zeilennummer, die es in der genannten Datei nicht gibt.
	for _, src := range s.proseSources() {
		for i, line := range strings.Split(src.text, "\n") {
			low := strings.ToLower(line)
			idx := strings.Index(low, "blocked")
			if idx < 0 {
				continue
			}
			// Verneinungen sind der Normalfall in guten Configs — und stehen dort
			// selten direkt davor („ende mit done, NIE mit blocked"). Deshalb der
			// Blick auf das Satzstück vor dem Wort statt auf feste Wortpaare.
			if negatedBefore(low[:idx]) {
				continue
			}
			return []Finding{{
				AgentSlug: s.Slug, Rule: "blocked-bei-polling-system", Severity: SeverityInfo,
				File: src.name, Line: i + 1,
				Message: "Die Config erwähnt blocked, obwohl der Agent an einem Polling-Zielsystem hängt (gitlab/email)",
				Hint:    "Diese Systeme haben keinen Webhook, der eine geparkte Aufgabe weckt — dort mit done enden und beim nächsten Heartbeat erneut aufgreifen.",
			}}
		}
	}
	return nil
}

// proseSource ist ein prüfbarer Fließtext des Agenten samt seiner Herkunft.
type proseSource struct{ name, text string }

// proseSources sind die Texte, die das Verhalten beschreiben: die beiden
// Prompt-Dateien und die Skills. Skills nach Namen sortiert, damit derselbe
// Agent nicht mal diesen und mal jenen Befund liefert.
func (s Subject) proseSources() []proseSource {
	out := []proseSource{
		{"SOUL.md", s.Files["SOUL.md"]},
		{"PLAYBOOKS.md", s.Files["PLAYBOOKS.md"]},
	}
	names := make([]string, 0, len(s.Skills))
	for name := range s.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, proseSource{"skill:" + name, s.Skills[name]})
	}
	return out
}

// skillText klebt alle Skills zu einem Heuhaufen zusammen — für Regeln, die
// nur suchen, ob etwas irgendwo vorkommt.
func (s Subject) skillText() string {
	var b strings.Builder
	for _, src := range s.proseSources()[2:] {
		b.WriteString(src.text)
		b.WriteString("\n")
	}
	return b.String()
}

// negationTokens verneinen eine blocked-Erwähnung im selben Satz.
var negationTokens = []string{"nie", "niemals", "nicht", "kein", "statt", "ohne"}

// negatedBefore sagt, ob im Satzstück vor einer Erwähnung eine Verneinung
// steht. Betrachtet wird nur der aktuelle Satz — ein „nicht" zwei Sätze vorher
// hat mit dieser Aussage nichts zu tun.
func negatedBefore(prefix string) bool {
	if cut := strings.LastIndexAny(prefix, ".;"); cut >= 0 {
		prefix = prefix[cut+1:]
	}
	for _, w := range strings.FieldsFunc(prefix, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !strings.ContainsRune("äöüß", r)
	}) {
		for _, n := range negationTokens {
			if w == n {
				return true
			}
		}
	}
	return false
}

// lintStages prüft das Board: Spalten sollen Arbeitszustände benennen. Ein
// Name mit Vorgangs-ID passt auf genau eine Aufgabe und bleibt danach als tote
// Spalte stehen; viele Spalten heißen meist, dass der Agent Tagebuch führt.
func lintStages(s Subject) []Finding {
	var out []Finding
	for _, name := range s.AgentStages {
		if stageWithItemID.MatchString(name) {
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "stage-benennt-vorgang", Severity: SeverityWarn,
				Message: fmt.Sprintf("Board-Spalte %q benennt einen Vorgang statt eines Arbeitszustands", name),
				Hint:    "Spaltennamen gelten für jede Aufgabe („Analyse\"), nicht für eine („#83 CSV-Import\"). Feste Spaltenliste in SOUL.md vorgeben.",
			})
		}
	}
	if n := len(s.AgentStages); n > maxAgentStages {
		out = append(out, Finding{
			AgentSlug: s.Slug, Rule: "stage-wildwuchs", Severity: SeverityWarn,
			Message: fmt.Sprintf("%d selbst angelegte Board-Spalten", n),
			Hint:    "Ein halbes Dutzend reicht für jeden Ablauf. Mehr heißt meist: verschiedene Namen für denselben Zustand. Feste Spaltenliste in SOUL.md vorgeben.",
		})
	}
	return out
}

// lintTurnLimit meldet Läufe, die am Turn-Limit abgebrochen sind. Einzelne sind
// normal — die Plattform fängt sie mit einer Folgeaufgabe auf. Häufen sie sich,
// ist der Auftrag zu groß geschnitten oder max_turns zu klein.
func lintTurnLimit(s Subject) []Finding {
	if s.TurnLimitFailures < 3 {
		return nil
	}
	hint := "Auftrag kleiner schneiden (Playbook-Schritt: Teilergebnis abschließen, Rest per covey/create_task anlegen)"
	if s.MaxTurns > 0 && s.MaxTurns < 50 {
		hint += fmt.Sprintf(" oder max_turns anheben (aktuell %d)", s.MaxTurns)
	}
	return []Finding{{
		AgentSlug: s.Slug, Rule: "haeufige-turn-limit-abbrueche", Severity: SeverityWarn,
		Message: fmt.Sprintf("%d Läufe sind am Turn-Limit abgebrochen", s.TurnLimitFailures),
		Hint:    hint + ".",
	}}
}

func joinHeavy(systems map[string]bool) string {
	var names []string
	for name := range systems {
		if heavySystems[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// shortDur formatiert ein Intervall so, wie es in HEARTBEAT.md steht (2m, 1h30m).
func shortDur(d time.Duration) string {
	return strings.TrimSuffix(d.String(), "0s")
}

// heartbeatLine findet die Zeile eines Heartbeats über seinen Titel. Der Parser
// gibt die Zeilennummer nicht mit (sie steht auch nicht in der persistierten
// Form) — für einen Befund, den ein Mensch nachschlagen soll, lohnt sie sich
// trotzdem. Ohne Treffer: 0, der Ausgabe fehlt dann nur die Nummer.
func heartbeatLine(content, name string) int {
	if name == "" {
		return 0
	}
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, name) {
			return i + 1
		}
	}
	return 0
}
