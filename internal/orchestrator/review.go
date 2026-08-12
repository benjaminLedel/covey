package orchestrator

// Review: die Meta-Actions, mit denen ein Agent einen Kollegen liest und für
// ihn eine Änderung vorschlägt (spec/21-operations-and-improvement.md).
//
// Die andere Hälfte von hiring.go, und bewusst mit einem eigenen Scope:
// `- system: covey scope: agents:review`. Er schaltet diese drei Aktionen frei
// und NICHT das Entwerfen; `agents:write` schaltet das Entwerfen frei und nicht
// diese. Ein Agent darf beide halten — die Personalabteilung und der
// Betriebsingenieur tun es nicht, und deshalb kann keiner von beiden mit den
// Zugängen des anderen dessen Arbeit machen.
//
// Fünf Regeln tragen sie, alle fünf hier durchgesetzt und nicht im Prompt:
//
//  1. ER SCHLÄGT VOR, ER SETZT NICHT IN KRAFT. propose_agent_config schreibt
//     eine inaktive Version. Es gibt von hier keinen Weg zu einer laufenden
//     Config — für keine Datei. Regel 4 aus spec/20 bleibt unangetastet:
//     set_agent_config wird nicht geweitet, die neue Aktion ist strikt
//     schwächer. Ein kompromittierter Betriebsingenieur erzeugt eine
//     Warteschlange schlechter Vorschläge, die ein Mensch ablehnt — ein
//     Ärgernis, kein Vorfall.
//  2. ER LIEST SEINE EIGENEN ZAHLEN NICHT. work_record erreicht den Aufrufer
//     nicht. Das ist derselbe Grund, aus dem die KPIS.md nicht in den
//     Systemprompt kompiliert wird (internal/agents/kpi.go): wer weiß, woran
//     er gemessen wird, arbeitet auf das Maß hin statt auf die Sache.
//
//     Der VORSCHLAG an sich selbst ist dagegen erlaubt — eine bewusste
//     Abweichung von der ersten Fassung der Regel. Der Grund, der sie trug,
//     trägt hier nicht: nichts von hier läuft, ein Mensch nimmt jeden
//     Vorschlag an oder lehnt ihn ab. Damit ist auch der offene Punkt aus
//     spec/20 geschlossen — die Personalabteilung darf nach ihrem
//     Self-Onboarding ihre eigene Konfiguration vorschlagen. Mit
//     `agents:write` allein aber NUR die eigene: für die eines Kollegen
//     braucht es `agents:review`, sonst wäre der zweite Scope umgangen.
//  3. ER LIEST FAKTEN. Die Arbeitsakte ist, was die Control Plane selbst
//     aufgeschrieben hat. Ein Gespräch — ein Recording — ist nur über eine
//     Freigabe erreichbar, ein Lauf auf einmal, und die Freigabe ist an genau
//     diesen Lauf gebunden.
//  4. NICHTS SONST ÜBER EINEN KOLLEGEN ist erreichbar: nicht seine Secrets,
//     nicht seine Guard-Rails, nicht seine Runtime, nicht sein Budget, nicht
//     sein Notaus. Es gibt keine Aktion dafür.
//  5. ES GIBT KEIN `fire`. Keine verbotene — eine fehlende, damit es nichts zu
//     vergessen gibt. Dieser Agent darf sagen, dass ein Kollege nicht
//     funktioniert. Das Arbeitsverhältnis zu beenden ist die Handlung eines
//     Menschen, so wie es der Beginn ist.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/workrecord"
)

// maxRecordingEvents begrenzt, was EIN Lauf zurückgibt. Ein Recording ist der
// teure Teil der Akte, und ein Lauf, der über diese Grenze geht, ist selbst
// schon der Befund.
const maxRecordingEvents = 400

// reviewTarget löst den Agenten auf, um den es geht — innerhalb der eigenen
// Organisation. allowSelf trennt die beiden Hälften von Regel 2: die eigene
// Arbeitsakte bleibt zu (er soll seine Zahlen nicht kennen), der eigene
// Vorschlag ist offen (ein Mensch entscheidet ihn ohnehin).
func (o *Orchestrator) reviewTarget(ctx context.Context, agent agents.Agent, slug string,
	allowSelf bool) (agents.Agent, string) {

	slug = strings.TrimSpace(slug)
	if slug == "" {
		return agents.Agent{}, "agent is missing (the slug of the agent this is about)"
	}
	other, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
	if err != nil {
		return agents.Agent{}, "no agent \"" + slug + "\" in this organisation"
	}
	if other.ID == agent.ID && !allowSelf {
		return agents.Agent{}, "you do not read your own record — an agent that knows " +
			"what it is measured on works towards the measure. Read a colleague's."
	}
	return other, ""
}

// reviewWorkRecord gibt die Arbeitsakte eines Kollegen heraus: Fakten, die die
// Control Plane selbst aufgeschrieben hat, je Agent und Zeitraum.
func (o *Orchestrator) reviewWorkRecord(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	days := req.Days
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	builder := &workrecord.Builder{Pool: o.Pool, Registry: o.Registry, Obs: o.Obs, Skills: o.lintSkills()}
	rec, err := builder.Build(ctx, other.ID, time.Now().AddDate(0, 0, -days))
	if err != nil {
		return fail("work record not readable: %v", err)
	}
	return ok(rec)
}

// reviewReadRecording gibt EINEN Lauf im Wortlaut heraus — und nur, nachdem
// ein Mensch zugestimmt hat.
//
// Die Freigabe entsteht schon vor dieser Funktion (hiring.go, AlwaysApprove);
// wer hier ankommt, hat sie. Was bleibt, ist die Prüfung, dass der Lauf zu dem
// Kollegen gehört, für den gefragt wurde: die Freigabe nennt einen Agenten und
// eine Aufgabe, und beides muss zusammenpassen, sonst hat ein Mensch etwas
// anderes freigegeben, als er gelesen hat.
func (o *Orchestrator) reviewReadRecording(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	readID, err := uuid.Parse(strings.TrimSpace(req.Task))
	if err != nil {
		return fail("task is missing or not an id (the run you want to read; the work record names it)")
	}
	task, err := o.Backlog.Get(ctx, readID)
	if err != nil || task.AgentID != other.ID {
		return fail("no run %q at agent %q", req.Task, other.Slug)
	}

	events, err := o.Obs.Events(ctx, other.ID, &readID, 0, maxRecordingEvents)
	if err != nil {
		return fail("recording not readable: %v", err)
	}
	// Das Lesen selbst gehört ins Recording des LESENDEN — und zwar von der
	// Control Plane geschrieben, nicht vom Sandbox-Proxy. Wer die Akte eines
	// Betriebsingenieurs liest, muss sehen, in welche Gespräche er geschaut
	// hat; sonst prüft man ihn an dem, was er geschrieben hat, ohne zu wissen,
	// was er gelesen hat. Als Lifecycle-Ereignis wie bei den Entwurfs-Aktionen:
	// die Aktion selbst schreibt der Proxy, die Herkunft schreibt die Plattform.
	//
	// Der Eintrag hängt an der EIGENEN Aufgabe (taskID), nicht am gelesenen Lauf
	// (readID): Obs.Events filtert immer über agent_id UND task_id, ein Eintrag
	// unter fremder Aufgabe wäre also im Recording des Lesenden unsichtbar — und
	// der Verweis führte in einen Lauf, der ihm nicht gehört. Welcher Lauf
	// gelesen wurde, steht daneben in "run".
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "recording_read", "about_agent": other.ID.String(),
			"slug": other.Slug, "run": readID.String(),
			"events": strconv.Itoa(len(events))})

	out := map[string]any{
		"agent": other.Slug, "task": readID.String(), "title": task.Title,
		"state": task.State, "events": events,
	}
	if len(events) == maxRecordingEvents {
		out["note"] = "truncated to the last 400 events of this run"
	}
	return ok(out)
}

// reviewPropose schreibt einen Vorschlag: eine gespeicherte Config-Version, die
// NICHT in Kraft ist. Ein Mensch nimmt sie an, oder sie bleibt liegen.
func (o *Orchestrator) reviewPropose(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, true)
	if reason != "" {
		return fail("%s", reason)
	}
	// Der Selbstvorschlag ist die eine Ausnahme, die `agents:write` allein
	// trägt (spec/20): wer entwirft, darf nach seinem Self-Onboarding seine
	// eigene Konfiguration vorschlagen. Für die eines KOLLEGEN braucht es den
	// Review-Scope — sonst hätte die Personalabteilung sich über die
	// Hintertür genau die Reichweite geholt, die zwei Scopes verhindern sollen.
	if other.ID != agent.ID && !o.mayUseCovey(ctx, agent, scopeReview) {
		return fail("%s", "with `scope: "+scopeWrite+"` you may propose only your OWN "+
			"configuration — proposing for a colleague needs `scope: "+scopeReview+"`")
	}
	if len(req.Files) == 0 {
		return fail("files is missing (file name → complete content, only the files you change)")
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return fail("title is missing (one line: what you propose)")
	}
	if strings.TrimSpace(req.Rationale) == "" {
		return fail("rationale is missing — a proposal without the observation behind it " +
			"is one a human cannot decide on")
	}
	// Regel 2 aus spec/20 gilt auch hier: ein Vorschlag darf keinem Kollegen
	// das eigene System der Plattform verschaffen. Sonst wäre der Weg um Regel
	// 2 herum ein angenommener Vorschlag.
	if acc, ok := req.Files["ACCESS.md"]; ok {
		for _, a := range agents.ParseAccess(acc) {
			if a.System == hiringSystem {
				return fail("a proposal may not give a colleague the system `covey` — " +
					"who may reach the platform itself is decided by a human, not proposed")
			}
		}
	}

	item, err := o.Registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: agent.OrgID, AgentID: other.ID, Kind: agents.KindProposal,
		Title: title, Rationale: req.Rationale, Files: req.Files,
		AuthorAgentID: &agent.ID, TaskID: &taskID,
	})
	if err != nil {
		return fail("%v", err)
	}
	// Herkunft schreibt die Plattform, nicht das Modell: aus welcher Aufgabe
	// ein Vorschlag kam, steht hier und nicht in einer Meldung.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "config_proposed", "about_agent": other.ID.String(),
			"slug": other.Slug, "proposal": item.ID.String()})

	return ok(map[string]any{
		"proposal": item.ID.String(), "agent": other.Slug,
		"base_version": item.BaseVersion, "files": sortedKeys(req.Files),
		"note": "The proposal is stored and NOT in effect. A human accepts it, or it stays where it is.",
	})
}

// reviewWrite hält die Beurteilung fest — und mit ihr die Punkte, die aus ihr
// hervorgingen und die nur ein Mensch erledigen kann.
//
// EIN Aufruf für beides, weil es ein Urteil ist: der Text sagt, was er gesehen
// hat, der Befund sagt, wer es beheben muss, das Issue sagt, wo es schon liegt.
// In zwei Aufrufen zerfiele das in einen Bericht ohne Folgen und Folgen ohne
// Bericht — und zwischen den beiden kann ein Lauf am Turn-Limit enden.
//
// Das Review wartet auf nichts. Es geht nicht in den Vorrat der offenen
// Punkte, sondern auf die Seite des Kollegen; die Befunde und Issues gehen in
// beides, denn sie brauchen jemanden.
func (o *Orchestrator) reviewWrite(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent, false)
	if reason != "" {
		return fail("%s", reason)
	}
	if strings.TrimSpace(req.Summary) == "" {
		return fail("summary is missing — the review IS the text; a colleague " +
			"with three proposals and no assessment is a diff without a diagnosis")
	}
	days := req.Days
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	rev, err := o.Registry.CreateReview(ctx, agents.Review{
		OrgID: agent.OrgID, AgentID: other.ID, AuthorAgentID: &agent.ID, TaskID: &taskID,
		PeriodFrom: now.AddDate(0, 0, -days), PeriodTo: now, Summary: req.Summary,
	})
	if err != nil {
		return fail("%v", err)
	}

	// Befunde und Issues als offene Punkte. Ein Befund, den niemand abhaken
	// muss, ist eine Nachricht — und Nachrichten gehen unter (spec/21).
	angelegt := 0
	for _, spec := range []struct {
		kind  string
		notes []daemon.ReviewNote
	}{{agents.KindFinding, req.Findings}, {agents.KindIssue, req.Issues}} {
		for _, n := range spec.notes {
			if strings.TrimSpace(n.Title) == "" {
				continue
			}
			if _, err := o.Registry.CreateImprovement(ctx, agents.ImprovementItem{
				OrgID: agent.OrgID, AgentID: other.ID, Kind: spec.kind,
				Title: n.Title, Rationale: n.Detail, Link: n.Link,
				AuthorAgentID: &agent.ID, TaskID: &taskID,
			}); err != nil {
				o.Log.Warn("review: item not stored", "agent", other.Slug, "kind", spec.kind, "err", err)
				continue
			}
			angelegt++
		}
	}

	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "review_written", "about_agent": other.ID.String(),
			"slug": other.Slug, "review": rev.ID.String(), "items": strconv.Itoa(angelegt)})

	return ok(map[string]any{
		"review": rev.ID.String(), "agent": other.Slug, "items": angelegt,
		"note": "The review is on the colleague's profile, dated. It does not reach the " +
			"colleague itself by any path — findings and issues wait for a human.",
	})
}

// lintSkills gibt dem Config-Lint der Arbeitsakte die Skills des Agenten.
// Ohne sie prüfte er eine halbe Config: Verfahren, die aus der PLAYBOOKS.md in
// einen Skill gewandert sind, wären unsichtbar, und Regeln wie „wer arbeitet,
// kommentiert" schlügen falsch an — eine Prüfung, die gute Configs anmeckert,
// wird ignoriert.
//
// nil, wenn die Instanz ohne Skill-Store läuft; die Regeln, die sie brauchen,
// fallen dann weg.
func (o *Orchestrator) lintSkills() agents.SkillLookup {
	if o.Skills == nil {
		return nil
	}
	return func(ctx context.Context, orgID, agentID uuid.UUID) (map[string]string, error) {
		found, err := o.Skills.ForAgent(ctx, orgID, agentID)
		if err != nil {
			return nil, err
		}
		out := make(map[string]string, len(found))
		for _, sk := range found {
			var b strings.Builder
			for _, f := range sk.Files {
				b.WriteString(f.Content)
				b.WriteString("\n")
			}
			out[sk.Name] = b.String()
		}
		return out, nil
	}
}
