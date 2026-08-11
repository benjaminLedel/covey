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
//  2. ER BEGUTACHTET SICH NICHT SELBST. Weder work_record noch
//     propose_agent_config erreichen den Aufrufer. Ein Agent, der sich nachts
//     selbst benotet, ist die Tür, die diese Plattform zuhält — und einer, der
//     seine eigenen Zahlen liest, schreibt danach für die Zahlen.
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

// reviewTarget löst den Kollegen auf, um den es geht — innerhalb der eigenen
// Organisation, und niemals der Aufrufer selbst.
//
// Die Selbst-Prüfung steht hier und nicht in drei Aufrufern: sie ist Regel 2,
// und eine Regel, die an drei Stellen wiederholt wird, fehlt irgendwann an
// einer vierten.
func (o *Orchestrator) reviewTarget(ctx context.Context, agent agents.Agent, slug string) (agents.Agent, string) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return agents.Agent{}, "agent is missing (the slug of the colleague this is about)"
	}
	other, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
	if err != nil {
		return agents.Agent{}, "no agent \"" + slug + "\" in this organisation"
	}
	if other.ID == agent.ID {
		return agents.Agent{}, "you do not review yourself — read a colleague's record, not your own"
	}
	return other, ""
}

// reviewWorkRecord gibt die Arbeitsakte eines Kollegen heraus: Fakten, die die
// Control Plane selbst aufgeschrieben hat, je Agent und Zeitraum.
func (o *Orchestrator) reviewWorkRecord(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent)
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
func (o *Orchestrator) reviewReadRecording(ctx context.Context, agent agents.Agent, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	other, reason := o.reviewTarget(ctx, agent, req.Agent)
	if reason != "" {
		return fail("%s", reason)
	}
	taskID, err := uuid.Parse(strings.TrimSpace(req.Task))
	if err != nil {
		return fail("task is missing or not an id (the run you want to read; the work record names it)")
	}
	task, err := o.Backlog.Get(ctx, taskID)
	if err != nil || task.AgentID != other.ID {
		return fail("no run %q at agent %q", req.Task, other.Slug)
	}

	events, err := o.Obs.Events(ctx, other.ID, &taskID, 0, maxRecordingEvents)
	if err != nil {
		return fail("recording not readable: %v", err)
	}
	// Das Lesen selbst gehört ins Recording des LESENDEN — und zwar von der
	// Control Plane geschrieben, nicht vom Sandbox-Proxy. Wer die Akte eines
	// Betriebsingenieurs liest, muss sehen, in welche Gespräche er geschaut
	// hat; sonst prüft man ihn an dem, was er geschrieben hat, ohne zu wissen,
	// was er gelesen hat. Als Lifecycle-Ereignis wie bei den Entwurfs-Aktionen:
	// die Aktion selbst schreibt der Proxy, die Herkunft schreibt die Plattform.
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "recording_read", "about_agent": other.ID.String(),
			"slug": other.Slug, "run": taskID.String(),
			"events": strconv.Itoa(len(events))})

	out := map[string]any{
		"agent": other.Slug, "task": taskID.String(), "title": task.Title,
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

	other, reason := o.reviewTarget(ctx, agent, req.Agent)
	if reason != "" {
		return fail("%s", reason)
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
