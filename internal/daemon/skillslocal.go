package daemon

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Materializing skills in the agent home.
//
// Claude Code loads personal skills from ~/.claude/skills/<name>/SKILL.md. The
// daemon starts the run with HOME=<agent home> (runtime_claudecode.go), so a
// directory written there becomes a personal skill of exactly this agent —
// without Covey having to change anything about the prompt.
//
// The gain lies in load time: the description is always in the context, body
// and additional files are only read by the runtime once the skill applies.
// That is precisely why procedures belong here and not in PLAYBOOKS.md, which
// every run carries along in full.
//
// Unlike the wiki there is no way back into the control plane. Skills are
// centrally managed config; a run that could write itself new capabilities
// would undermine the very control that justifies the feature.

// skillsDirName is the directory in which Claude Code looks for personal
// skills. It is the fallback for an engine that declares none; the path itself
// belongs to the ENGINE (RuntimeCapabilities.SkillsDir), because it is that
// engine's convention and another one looks elsewhere. Writing skills where
// nothing reads them is the worst failure mode available — configured, visible
// in the interface, without effect.
const skillsDirName = ".claude/skills"

// skillsRequestTimeout is deliberately shorter than the 30 s of the other broker
// calls. The request sits in the critical path BEFORE every run, and its answer
// is dispensable. If the control plane does not know request_skills (older than
// this feature — coveyd sits in the sandbox image and regularly lags behind
// during a rollout), no answer would ever come; without a tight bound EVERY run
// would then lose half a minute before it even starts.
const skillsRequestTimeout = 10 * time.Second

// requestSkills fetches the agent's skills from the control plane.
func (c *Client) requestSkills(ctx context.Context) (InjectSkills, error) {
	req := RequestSkills{RequestID: uuid.NewString()}
	reqCtx, cancel := context.WithTimeout(ctx, skillsRequestTimeout)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestSkills, req.RequestID, req)
	if err != nil {
		return InjectSkills{}, err
	}
	return DecodePayload[InjectSkills](msg)
}

// materializeSkills writes the agent's skills to <home>/.claude/skills/.
//
// Best-effort for the RUN: if it fails, the task runs on without skills instead
// of not at all. A missing skill costs quality, an aborted run costs the task.
//
// What is not best-effort is the cleanup. If the answer fails to arrive
// (timeout, transport error, a control plane that does not know request_skills
// yet), the directory is EMPTIED rather than left untouched. Because the home
// outlives the run: without this step the task would not run "without skills"
// but with the OLD ones — and a skill that was just withdrawn would keep
// working. "I do not know which ones apply" must have the same effect as "none";
// the control plane keeps the same promise by answering with an empty list even
// on database errors (orchestrator.skillsFor).
// Returns how many skills are effective for this run. The run needs the number
// because the Skill tool only belongs in the loading scope when there is
// anything to load (runtime.BuiltinTools) — every failure path therefore
// answers 0, exactly like the empty list.
func (c *Client) materializeSkills(ctx context.Context, engine string) int {
	// The path is the engine's convention. An engine that declares none knows
	// no skills — then nothing is written, because a skill in a directory the
	// engine never reads is worse than no skill: it looks configured.
	sub := skillsDirName
	if d, ok := Describe(engine); ok {
		if d.Capabilities.SkillsDir == "" {
			return 0
		}
		sub = d.Capabilities.SkillsDir
	}
	dir := filepath.Join(c.homeDir, filepath.FromSlash(sub))
	res, err := c.requestSkills(ctx)
	if err != nil || !res.OK {
		reason := "unknown"
		if err != nil {
			reason = err.Error()
		} else if res.Error != "" {
			reason = res.Error
		}
		c.log.Warn("skills not retrievable — running without skills", "err", reason)
		if err := clearSkillDirs(dir); err != nil {
			c.log.Warn("old skills could not be cleared", "err", err)
		}
		return 0
	}
	if err := writeSkillDirs(dir, res.Skills); err != nil {
		c.log.Warn("skills could not be materialized — running without skills", "err", err)
		return 0
	}
	return len(res.Skills)
}

// clearSkillDirs removes everything Covey previously put under dir, without
// writing anything new. A missing directory is not an error — then there never
// were any skills.
func clearSkillDirs(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// writeSkillDirs creates the skill directories and first removes everything
// Covey wrote there before.
//
// The cleanup is the reason why this does not simply write: the home outlives
// the run (warm_sandbox, persistent /home). A skill deleted in the control plane
// or detached from an agent would otherwise stay effective forever — withdrawing
// a capability would not take hold.
func writeSkillDirs(dir string, skills []SkillDir) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep := make(map[string]bool, len(skills))
	for _, s := range skills {
		if strings.TrimSpace(s.Name) != "" {
			keep[s.Name] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}

	for _, s := range skills {
		name := strings.TrimSpace(s.Name)
		if name == "" || !safeSkillName(name) {
			continue // the control plane validates; this is just the second door
		}
		skillDir := filepath.Join(dir, name)
		// Remove first, then write anew: that way files a skill had in an
		// earlier version and no longer has disappear as well.
		if err := os.RemoveAll(skillDir); err != nil {
			return err
		}
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			return err
		}
		for rel, content := range s.Files {
			if !safeSkillPath(rel) {
				continue // traversal — the control plane forbids it, and so do we
			}
			if rel == skillEntryFile {
				// The frontmatter is produced here so that name and description
				// are maintained in one place only (see skills.Render).
				content = renderSkillEntry(name, s.Description, content)
			}
			target := filepath.Join(skillDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

const skillEntryFile = "SKILL.md"

// safeSkillName/safeSkillPath are the second door in front of the file system.
// The control plane already validates (internal/skills), but the daemon writes —
// and whoever writes, checks. Deliberately duplicated: the daemon package must
// not import internal/skills (layer separation, the daemon runs in the sandbox
// and does not know the control plane).
func safeSkillName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func safeSkillPath(rel string) bool {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") ||
		strings.Contains(rel, `\`) || strings.Contains(rel, ":") {
		return false
	}
	cleaned := path.Clean(rel)
	if cleaned != rel || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// renderSkillEntry builds the SKILL.md including its frontmatter. Same rules as
// skills.Render in the control plane — in particular the quoting, because almost
// every description contains a colon and the block would otherwise be read as a
// YAML map.
func renderSkillEntry(name, description, body string) string {
	var b strings.Builder
	b.WriteString("---\nname: " + skillScalar(name) + "\n")
	b.WriteString("description: " + skillScalar(description) + "\n---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func skillScalar(v string) string {
	v = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " "))
	if v == "" {
		return `""`
	}
	risky := strings.ContainsAny(v, `:#"'{}[]&*!|>%@`+"`") ||
		strings.HasPrefix(v, "-") || strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ")
	if !risky {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}
