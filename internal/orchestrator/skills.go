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
// skillsPayloadBudget is the room the skills of one agent get in the answer —
// below the daemon's 16 MiB read limit, with space for JSON and escaping.
const skillsPayloadBudget = 12 << 20

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
	budget := skillsPayloadBudget
	for _, sk := range found {
		dir := daemon.SkillDir{Name: sk.Name, Description: sk.Description,
			Frontmatter: skills.RenderExtra(sk.Meta),
			Files:       make(map[string]string, len(sk.Files))}
		size := 0
		for _, f := range sk.Files {
			size += len(f.Content)
			if f.Binary() {
				if dir.Binary == nil {
					dir.Binary = map[string]string{}
				}
				dir.Binary[f.Path] = f.Content
				continue
			}
			dir.Files[f.Path] = f.Content
		}
		// All skills travel in one daemon message, and the daemon reads at most
		// 16 MiB. A skill that would push the answer past that is left out with
		// a warning — rather one skill missing than every skill lost to a
		// message the daemon refuses.
		if size > budget {
			o.Log.Warn("skipped skill — the skills of this agent exceed one daemon message",
				"agent", agent.Slug, "skill", sk.Name, "bytes", size)
			continue
		}
		budget -= size
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
