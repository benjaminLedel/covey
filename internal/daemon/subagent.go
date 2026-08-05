package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"covey/internal/target"
)

// A sub-run is a nested runtime run that starts INSIDE the project checkout
// instead of the agent home. That way the project's own Claude Code harness
// applies — CLAUDE.md as project memory, `.claude/agents`, skills and commands —
// which the outer run never sees because it runs from the home.
//
// The division of roles behind it (spec/12): the outer agent orchestrates and
// communicates (triage, issue/MR traffic, commit, memory), the sub-agent
// programs. It therefore deliberately reaches no target system: no
// COVEY_ACTION_PORT — and, thanks to childEnv, not the daemon's credentials for
// the control plane either, with which the broker could be addressed directly.
// It can read, change, build and test — nothing more.
const (
	// defaultSubAgentTurns is more generous than the outer run's limit: the
	// sub-agent does the actual work (understand, change, test).
	defaultSubAgentTurns = 60
	maxSubAgentTurns     = 200
)

// subAgentPrompt is the platform's only contribution to the sub-run's prompt.
// Deliberately terse: the project's harness should dominate, not Covey.
const subAgentPrompt = `You are working in the checkout of a project and are responsible for exactly one work order.
This project's conventions apply: follow CLAUDE.md, CONTRIBUTING and the rules,
skills and subagents the project brings along.

The frame of your work:
- You have NO access to GitLab, e-mail or other target systems, and you cannot push.
  Local git commits in the checkout are fine (follow the project's conventions);
  checking in to the target system is done by the agent that commissioned you.
- Verify your change before you report it finished: run the project's build and tests,
  and for a fix add a test if you can.
- Your last message is your report to the commissioning agent. Summarize in it:
  the cause, what you changed (file:line), how you verified it (which commands, which
  result) and what remained open. No status marker, no boilerplate — the report is the handover.`

// subAgentRunner binds the runner to the running task (for recording and cost
// attribution) and returns it in the shape the target port expects.
func (c *Client) subAgentRunner(taskID string) target.SubAgentRunner {
	return func(ctx context.Context, req target.SubAgentRequest) (target.SubAgentResult, error) {
		return c.runSubAgent(ctx, taskID, req)
	}
}

// runSubAgent drives a nested runtime run in the given directory. Events and
// cost travel over the same protocol messages as the outer run: the timeline
// shows the sub-run (marked), and the control plane's AddCost/enforceBudget
// apply here too — so a sub-run cannot bypass the budget.
func (c *Client) runSubAgent(ctx context.Context, taskID string, req target.SubAgentRequest) (target.SubAgentResult, error) {
	task := strings.TrimSpace(req.Task)
	if task == "" {
		return target.SubAgentResult{}, fmt.Errorf("task missing: the sub-agent needs a work order")
	}
	dir := req.Dir
	if dir == "" {
		return target.SubAgentResult{}, fmt.Errorf("cwd missing: the sub-agent starts in the project checkout")
	}
	dir = filepath.Clean(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.homeDir, dir)
	}
	// Check the path up front instead of letting the run fail at the
	// subprocess's chdir: "starting claude: chdir …" does not tell the agent
	// what it did wrong.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return target.SubAgentResult{}, fmt.Errorf(
			"cwd %q is not a directory in the sandbox — take the path from the checkout result", req.Dir)
	}
	// One sub-run per directory. The action proxy serves every request in its
	// own goroutine and the runtime does call tools in parallel: two runs in the
	// same checkout overwrote each other's files and both reported the same
	// cumulative state — two reports about one blended piece of work. Hence a
	// refusal instead of a queue: the agent should read the first report before
	// it gives the next order in the same checkout. Different directories keep
	// running in parallel.
	if err := c.claimSubAgentDir(dir); err != nil {
		return target.SubAgentResult{}, err
	}
	defer c.releaseSubAgentDir(dir)

	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()

	runtime := c.runtimes[cfg.Runtime]
	if runtime == nil {
		return target.SubAgentResult{}, fmt.Errorf("unknown runtime %q", cfg.Runtime)
	}

	turns := req.MaxTurns
	if turns <= 0 {
		turns = defaultSubAgentTurns
	}
	if turns > maxSubAgentTurns {
		turns = maxSubAgentTurns
	}
	model := req.Model
	if model == "" {
		model = cfg.Model
	}

	// Anchor for the report: the upstream state that the checkout pins down as a
	// tag. If it is missing (the directory does not come from a checkout), the
	// state immediately before the sub-run serves as the anchor — never the root
	// commit, otherwise in a real clone the entire repo history would be "work".
	base := gitRev(ctx, dir, target.BaselineRef)
	if base == "" {
		base = gitRev(ctx, dir, "HEAD")
	}

	spec := RunSpec{
		TaskID:       taskID,
		Title:        "Work order in the project",
		Body:         task,
		SystemPrompt: subAgentPrompt,
		Model:        model,
		AllowedTools: cfg.AllowedTools,
		MaxTurns:     turns,
		HomeDir:      c.homeDir,
		WorkDir:      dir,
		// Deliberately without COVEY_ACTION_PORT: hermetic, no target systems.
		Env: c.runtimeKeyEnv(),
	}

	// Every sub-run gets an identifier of its own. It is what the timeline uses
	// to recognize a run's lines — not their adjacency in the stream. The
	// difference matters because the action proxy serves concurrently (one
	// goroutine per request): two simultaneous `dev agent` calls interleave their
	// lines under the same task, and without an identifier they would either
	// merge into one block or fall apart into fragments.
	stamp := subAgentStamper(dir, strconv.FormatUint(c.subRuns.Add(1), 10), task)
	res, err := runtime.Run(ctx, spec, func(kind string, payload json.RawMessage) {
		_ = c.send(TypeEvent, Event{TaskID: taskID, Kind: kind, Payload: stamp(payload)})
	})
	if err != nil {
		return target.SubAgentResult{}, err
	}
	if res.CostUSD > 0 || res.TotalInputTokens() > 0 {
		_ = c.send(TypeCost, Cost{TaskID: taskID, USD: res.CostUSD,
			InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
			CacheReadTokens: res.CacheReadTokens, CacheCreationTokens: res.CacheCreationTokens,
			Model: res.Model})
	}

	changed, deleted := gitChangesSince(ctx, dir, base)
	return target.SubAgentResult{
		// At the turn limit the adapter delivers the aborted session's handover
		// state instead of a result (status "incomplete", see
		// runtime_claudecode.go). Exactly that belongs in the report: the
		// commissioning agent closes with the partial result and files the rest
		// as a task, instead of losing half the work.
		Result:         res.Result,
		ChangedFiles:   changed,
		Deleted:        deleted,
		CostUSD:        res.CostUSD,
		Error:          res.Error,
		TurnsExhausted: res.Status == "incomplete",
	}, nil
}

// claimSubAgentDir reserves a directory for the duration of a sub-run and
// rejects a second run in it while the first is working.
func (c *Client) claimSubAgentDir(dir string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subAgentDirs == nil {
		c.subAgentDirs = map[string]bool{}
	}
	if c.subAgentDirs[dir] {
		return fmt.Errorf("a sub-agent is already working in %s — read its report first "+
			"before giving the next order in the same checkout", dir)
	}
	c.subAgentDirs[dir] = true
	return nil
}

func (c *Client) releaseSubAgentDir(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subAgentDirs, dir)
}

// maxMarkedTask caps the work order inside the mark. There it serves as a
// heading, not as an archive — the full text is kept by the action proxy's
// action event anyway.
const maxMarkedTask = 400

// markSubAgent marks a runtime line as part of a sub-run — as an additional key
// IN the object, not as a wrapper around it. The difference is not cosmetic:
// recording and timeline read the runtime's format (stream-json) directly, and a
// wrapper would hide `type`. The sub-run would then appear as a JSON blob in the
// recording instead of as a turn with its tool calls — precisely where the
// actual work happens.
//
// run is the run's identifier, task the work order. task appears on the first
// line only (see subAgentStamper): that way the timeline shows at the head of
// the block WHAT the sub-agent was commissioned with, without having to expand
// it. The second return value says whether the line was really marked — only
// then does the order count as placed.
func markSubAgent(payload json.RawMessage, dir, run, task string) (json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return payload, false // not a JSON object → pass through unchanged
	}
	fields := map[string]string{"dir": dir, "run": run}
	if task = strings.TrimSpace(task); task != "" {
		if r := []rune(task); len(r) > maxMarkedTask {
			task = string(r[:maxMarkedTask]) + "…"
		}
		fields["task"] = task
	}
	mark, err := json.Marshal(fields)
	if err != nil {
		return payload, false
	}
	obj["covey_sub_agent"] = mark
	marked, err := json.Marshal(obj)
	if err != nil {
		return payload, false
	}
	return marked, true
}

// subAgentStamper marks the lines of ONE sub-run and hands out the work order
// exactly once while doing so — on every line it would enter the recording with
// its length × the number of lines, whereas as a heading it suffices once.
//
// It is only consumed once it is actually placed: a line that is not a JSON
// object is deliberately passed through by the adapter (stream tolerates it),
// and the order must not get lost on such a line.
//
// The returned stamper is NOT safe for concurrent use — nor does it need to be:
// stream calls the callback sequentially in its scanner loop, and a possible
// extra run (summarize) only follows afterwards.
func subAgentStamper(dir, run, task string) func(json.RawMessage) json.RawMessage {
	head := task
	return func(payload json.RawMessage) json.RawMessage {
		marked, stamped := markSubAgent(payload, dir, run, head)
		if stamped {
			head = ""
		}
		return marked
	}
}

// gitRev resolves a reference to a commit. Empty if it does not exist (no repo,
// no commit, no tag) — the caller then decides whether to take another anchor or
// report no file list at all.
func gitRev(ctx context.Context, dir, rev string) string {
	out, err := gitRun(ctx, dir, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitChangesSince returns the work in the checkout as the two lists the commit
// action expects: changed/new and deleted files, each repo-relative and measured
// against base.
//
// Measured against a COMMIT, not against a `git status` snapshot taken earlier.
// That is the point: the sub-agent may commit locally in the checkout — many
// projects demand it in their CLAUDE.md — and after a commit `git status` shows
// nothing any more. The work would then sit finished on disk, but the report
// would be empty and the commit action would abort with "nothing to commit".
//
// Hence both halves together: what has been committed since base (git diff) and
// what lies open beside it in the working tree (git status). Cache directories
// stay out via the checkout's .git/info/exclude.
func gitChangesSince(ctx context.Context, dir, base string) (changed, deleted []string) {
	if base == "" {
		return nil, nil // no anchor → rather no list than a wrong one
	}
	// path → deleted? The later source wins because it describes the more recent
	// state: first base→HEAD, then HEAD→working tree.
	state := map[string]bool{}
	mark := func(code, from, to string) {
		switch {
		case code == "" || from == "":
			return
		case strings.ContainsAny(code, "RC"):
			// Rename/copy: the target is new, and with R the source goes away.
			// The source must come along, otherwise it would remain in the
			// target system.
			if to != "" {
				state[to] = false
			}
			if strings.ContainsRune(code, 'R') {
				state[from] = true
			}
		case strings.ContainsRune(code, 'D'):
			state[from] = true
		default:
			state[from] = false
		}
	}
	// Committed: --name-status -z yields a field stream of status and path, with
	// two paths on rename/copy — "R100\0old\0new\0".
	fields := gitFields(ctx, dir, "diff", "--name-status", "-z", base, "HEAD")
	for i := 0; i+1 < len(fields); {
		code, from := fields[i], fields[i+1]
		i += 2
		to := ""
		if strings.ContainsAny(code, "RC") {
			if i >= len(fields) {
				break
			}
			to = fields[i]
			i++
		}
		mark(code, from, to)
	}
	// Open in the working tree: --porcelain -z. One field is one record
	// "XY <path>"; on rename/copy the source follows as a field of its own —
	// and AFTER the target, because with -z the order is reversed compared to
	// "old -> new".
	fields = gitFields(ctx, dir, "status", "--porcelain", "-z", "-uall")
	for i := 0; i < len(fields); {
		rec := fields[i]
		i++
		if len(rec) < 4 {
			continue
		}
		code, path := strings.TrimSpace(rec[:2]), rec[3:]
		if strings.ContainsAny(code, "RC") {
			if i >= len(fields) {
				break
			}
			mark(code, fields[i], path) // target first, source afterwards
			i++
			continue
		}
		mark(code, path, "")
	}

	for path, gone := range state {
		if gone {
			deleted = append(deleted, path)
			continue
		}
		changed = append(changed, path)
	}
	// Keep it stable — otherwise the order changes per run (map iteration) and
	// both recording and commit diff become needlessly noisy.
	sort.Strings(changed)
	sort.Strings(deleted)
	return changed, deleted
}

// gitRun executes a git command in the checkout. Two details are baked in here,
// neither of them cosmetic:
//
//   - core.quotepath=false: otherwise git (default true) escapes paths outside
//     ASCII — "pr\303\274fung.go" instead of prüfung.go. The path would go
//     mangled into changed_files and from there unchanged into the commit
//     action. Together with -z at the caller the bytes come out raw and
//     unquoted; that takes care of spaces and quotes as well.
//   - childEnv: without the daemon's COVEY_* variables. git reads configuration
//     FROM the repository, and that config can name commands git itself executes
//     (core.fsmonitor, filter drivers). After the sub-run this repository is no
//     more trustworthy than the rest of the checkout.
func gitRun(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotepath=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = childEnv()
	return cmd.Output()
}

// gitFields executes a git command with -z and returns its NUL-separated
// fields. Empty without git or without a repository: the sub-run then simply
// reports no file list instead of failing.
func gitFields(ctx context.Context, dir string, args ...string) []string {
	out, err := gitRun(ctx, dir, args...)
	if err != nil {
		return nil
	}
	trimmed := strings.TrimRight(string(out), "\x00")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\x00")
}
