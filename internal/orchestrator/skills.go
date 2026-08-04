package orchestrator

import (
	"context"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/skills"
)

// skillsFor answers request_skills: the skills this agent actually gets — its
// own plus the ones linked to it from the org library. The daemon writes them
// to <home>/.claude/skills/, where the runtime finds them as the agent's
// personal skills.
//
// Fail-soft: if the feature is not configured (Skills == nil) or the query
// fails, we answer with an EMPTY but successful list. The daemon then clears
// out the old directories and carries on without skills. That is the safe
// direction: better a run without an extra skill than a run with a skill that
// was revoked long ago.
func (o *Orchestrator) skillsFor(ctx context.Context, agent agents.Agent, req daemon.RequestSkills) daemon.InjectSkills {
	out := daemon.InjectSkills{RequestID: req.RequestID, OK: true, Skills: []daemon.SkillDir{}}
	if o.Skills == nil {
		return out
	}
	found, err := o.Skills.ForAgent(ctx, agent.OrgID, agent.ID)
	if err != nil {
		o.Log.Warn("skills could not be resolved", "agent", agent.Slug, "err", err)
		return out
	}
	for _, sk := range found {
		dir := daemon.SkillDir{Name: sk.Name, Description: sk.Description,
			Files: make(map[string]string, len(sk.Files))}
		for _, f := range sk.Files {
			dir.Files[f.Path] = f.Content
		}
		// A skill without SKILL.md would be no skill at all to the runtime — then
		// rather do not ship it than create a dead directory.
		if _, ok := dir.Files[skills.EntryFile]; !ok {
			o.Log.Warn("skipped skill without SKILL.md", "agent", agent.Slug, "skill", sk.Name)
			continue
		}
		out.Skills = append(out.Skills, dir)
	}
	return out
}
