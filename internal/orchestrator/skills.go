package orchestrator

import (
	"context"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/skills"
)

// skillsFor beantwortet request_skills: die Fähigkeiten, die dieser Agent
// tatsächlich bekommt — seine eigenen plus die ihm aus der Org-Bibliothek
// verlinkten. Der Daemon schreibt sie nach <home>/.claude/skills/, wo die
// Runtime sie als persönliche Skills des Agenten findet.
//
// Fail-soft: Ist das Feature nicht konfiguriert (Skills == nil) oder scheitert
// die Abfrage, antworten wir mit einer LEEREN, aber erfolgreichen Liste. Der
// Daemon räumt dann die alten Verzeichnisse ab und läuft ohne Skills weiter.
// Das ist die sichere Richtung: Lieber ein Lauf ohne Zusatzfähigkeit als ein
// Lauf mit einer Fähigkeit, die längst entzogen wurde.
func (o *Orchestrator) skillsFor(ctx context.Context, agent agents.Agent, req daemon.RequestSkills) daemon.InjectSkills {
	out := daemon.InjectSkills{RequestID: req.RequestID, OK: true, Skills: []daemon.SkillDir{}}
	if o.Skills == nil {
		return out
	}
	found, err := o.Skills.ForAgent(ctx, agent.OrgID, agent.ID)
	if err != nil {
		o.Log.Warn("skills nicht auflösbar", "agent", agent.Slug, "err", err)
		return out
	}
	for _, sk := range found {
		dir := daemon.SkillDir{Name: sk.Name, Description: sk.Description,
			Files: make(map[string]string, len(sk.Files))}
		for _, f := range sk.Files {
			dir.Files[f.Path] = f.Content
		}
		// Ein Skill ohne SKILL.md wäre für die Runtime kein Skill — dann lieber
		// gar nicht ausliefern, statt ein totes Verzeichnis anzulegen.
		if _, ok := dir.Files[skills.EntryFile]; !ok {
			o.Log.Warn("skill ohne SKILL.md übersprungen", "agent", agent.Slug, "skill", sk.Name)
			continue
		}
		out.Skills = append(out.Skills, dir)
	}
	return out
}
