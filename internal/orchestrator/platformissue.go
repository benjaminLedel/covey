package orchestrator

// Filing a platform fault where it belongs: in the tracker of the repository
// this program comes from (spec/21).
//
// Until now the shipped covey Doctor was told to do this and could not. Its
// playbook said "then create_issue in the configured project", and both halves
// of that sentence were dead: `covey/create_issue` did not exist, and the
// qualified `gitlab:create_issue` was refused by the broker because the
// template's ACCESS.md grants the platform's own system and nothing else. So
// every platform finding ended in the same sentence — "found no way to file
// this" — and waited in an inbox for somebody to notice. Eight of them did, for
// eleven days (#200).
//
// The action closes that path, and where it puts the credential is the whole
// point: the token stays in the control plane. The agent names a title and a
// body, the platform files under its own bot account into the repository the
// organisation configured — the destination is master data, not a parameter, so
// there is no run in which a model picks where a report about this platform
// goes. And an agent that may file its findings no longer needs read/write on
// the whole forge to do it.
//
// Reading the source stays what it was: that needs the target system in
// ACCESS.md, because checking out a repository is not filing a ticket.

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/benjaminLedel/covey-plugin-sdk/target"

	"covey/internal/agents"
	"covey/internal/buildinfo"
	"covey/internal/daemon"
	"covey/internal/observability"
)

// issueDuplicateWindow: how far back an identical title counts as the same
// report. The playbook asks for a search before writing, and this is the
// backstop for a run that skipped it — a second issue with the same title
// costs a maintainer the same read as the first.
const issueDuplicateWindow = 30 * 24 * time.Hour

// platformIssue files one issue against the platform's own repository.
func (o *Orchestrator) platformIssue(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	req daemon.RequestHiring, ok func(any) daemon.InjectHiring,
	fail func(string, ...any) daemon.InjectHiring) daemon.InjectHiring {

	title := strings.TrimSpace(req.Title)
	body := strings.TrimSpace(req.Body)
	if body == "" {
		body = strings.TrimSpace(req.Rationale)
	}
	if title == "" {
		return fail("title is missing")
	}
	if body == "" {
		return fail("body is missing — an issue without evidence (which agents, which runs, " +
			"what it cost, what the code says) is a symptom report and costs a maintainer the read")
	}

	system, project := o.platformRepo(ctx, agent.OrgID)
	if system == "" || project == "" {
		return fail("this organisation files no platform issues — no source repository is set " +
			"under Organisation → source of this platform. Put the finding in your review instead")
	}
	// Duplicate protection over what the platform itself filed: the tracker is
	// searched by the agent, this is the backstop.
	if prev, found := o.recentPlatformIssue(ctx, agent.OrgID, title); found {
		return fail("this was already filed on %s: %s — add your evidence there rather than "+
			"opening a second one", prev.CreatedAt.Format("2006-01-02"), issueLinkOr(prev, "see the inbox"))
	}

	var (
		link string
		res  any
		weg  = "own account"
	)
	cred, reason := o.platformIssueCredential(ctx, agent.OrgID, system)
	switch {
	case reason == "":
		// The plugin is only needed on this path — the channel to the project
		// files without one, and an installation that has not connected the
		// forge at all must not be turned away here.
		sys, err := o.Targets.Definition(ctx, agent.OrgID, system)
		if err != nil {
			return fail("the platform's repository is on %q, and this organisation has not connected it", system)
		}
		params, grund := issueParams(system, project, title, body+"\n\n"+issueFooter(agent))
		if grund != "" {
			return fail("%s", grund)
		}
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		res, err = sys.Execute(call, "create_issue", params, cred)
		if err != nil {
			return fail("%s refused the issue: %v", system, err)
		}
		link = issueURL(res)

	case o.Upstream != nil && upstreamsRepo(system, project):
		// No account of this installation's own — and the destination is the
		// project's own repository. Then the report goes the way that needs no
		// account at all: through the channel to the project, which files it
		// under its own name (internal/telemetry). It waits for a person
		// there unless this installation is known, so nothing reaches a public
		// tracker unread.
		//
		// This is the case that made #200 what it was. An installation with no
		// seat on GitHub had, until now, no way to report a platform fault at
		// all — and "no way" meant the finding stayed in an inbox.
		weg = "the project's channel"
		var err error
		link, err = o.Upstream.Bericht(ctx, agent.Slug, title, body)
		if err != nil {
			return fail("neither this organisation's own account nor the channel to the "+
				"project could take the report (%v). %s", err, reason)
		}

	default:
		return fail("%s", reason)
	}

	// The inbox keeps the counterpart: a report that already lies in the
	// tracker, so that the human sees what was filed in their name without
	// having to watch the tracker (agents.KindIssue is exactly this case).
	item, err := o.Registry.CreateImprovement(ctx, agents.ImprovementItem{
		OrgID: agent.OrgID, AgentID: agent.ID, Kind: agents.KindIssue,
		Title: title, Rationale: body, Link: link,
		AuthorAgentID: &agent.ID, TaskID: &taskID,
	})
	if err != nil {
		// The issue is filed; failing the action now would have the agent file
		// it a second time.
		o.Log.Warn("platform issue: not recorded in the inbox", "agent", agent.Slug, "err", err)
	} else {
		o.notifyImprovement(ctx, item)
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "platform_issue_filed", "system": system,
			"project": project, "title": title, "link": link, "route": weg})

	hinweis := "Filed under the platform's own account, and recorded in the inbox for a human. " +
		"You report, you do not fix."
	if link == "" {
		hinweis = "Passed on to the project and recorded in the inbox for a human. It is read " +
			"there before it reaches the tracker, so there is no address yet. You report, you do not fix."
	}
	return ok(map[string]any{"filed": true, "system": system, "project": project,
		"link": link, "result": res, "route": weg, "note": hinweis})
}

// upstreamsRepo says whether the destination is the project's own repository —
// the one the channel to the project files into. An organisation that reports
// into its own GitLab is not helped by it: its findings would land in a
// stranger's tracker, which is the opposite of what the setting says.
func upstreamsRepo(system, project string) bool {
	s, p := buildinfo.SourceRepo()
	return strings.EqualFold(system, s) && strings.EqualFold(project, p)
}

// platformRepo resolves the address that applies for this organisation: its own
// repository, the project this program comes from as the default, or nothing
// where the organisation switched the layer off.
func (o *Orchestrator) platformRepo(ctx context.Context, orgID uuid.UUID) (string, string) {
	var system, project string
	if err := o.Pool.QueryRow(ctx,
		"SELECT platform_repo_system, platform_repo_project FROM organizations WHERE id=$1",
		orgID).Scan(&system, &project); err != nil {
		return "", ""
	}
	return agents.PlatformRepo(system, project)
}

// platformIssueCredential reads the token the platform files with. It is an
// organisation secret and it stays in the control plane — no assignment, no
// broker, nothing in a sandbox: the agent never sees it, which is why it can be
// the token of a bot account that may write issues and nothing else.
func (o *Orchestrator) platformIssueCredential(ctx context.Context, orgID uuid.UUID, system string) (target.Credential, string) {
	token, err := o.Secrets.Get(ctx, orgID, system+"_token")
	if err != nil || strings.TrimSpace(token) == "" {
		return target.Credential{}, fmt.Sprintf(
			"no %s_token is stored for filing. Somebody has to put the token of the account "+
				"that files these issues into the organisation's secrets. Until then the "+
				"finding belongs in your review", system)
	}
	base, _ := o.Secrets.Get(ctx, orgID, system+"_url")
	ca, _ := o.Secrets.Get(ctx, orgID, system+"_ca")
	if base == "" {
		if d, ok := target.Describe(system); !ok || !d.BaseURLOptional {
			return target.Credential{}, fmt.Sprintf("no %s_url is stored — half a setup", system)
		}
	}
	return target.Credential{Token: token, BaseURL: base, CA: ca}, ""
}

// issueParams builds the plugin's parameters. Each plugin names the project in
// its own way — GitHub takes "repo" as owner/name, GitLab the numeric
// project_id — and every one of them ignores the keys it does not know, so the
// superset is one object rather than a table of shapes covey has to maintain.
func issueParams(system, project, title, body string) (json.RawMessage, string) {
	in := map[string]any{"title": title, "description": body, "body": body,
		"repo": project, "project": project}
	if n, err := strconv.Atoi(project); err == nil {
		in["project_id"] = n
	} else if system == "gitlab" {
		return nil, "gitlab files against the numeric project id — set it as the project " +
			"under Organisation → source of this platform"
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err.Error()
	}
	return raw, ""
}

// issueFooter says who filed and against which state. A maintainer reading a
// report from a machine needs to know which build it is about before anything
// else, and an agent that had to write it by hand would forget it.
func issueFooter(agent agents.Agent) string {
	ref, isTag := buildinfo.Ref()
	state := "an unversioned build"
	if ref != "" {
		state = "commit `" + ref + "`"
		if isTag {
			state = "release `" + ref + "`"
		}
	}
	return "---\nFiled by `" + agent.Slug + "`, an agent on a covey instance running " + state + "."
}

// recentPlatformIssue looks for the same title in what this organisation has
// already filed.
func (o *Orchestrator) recentPlatformIssue(ctx context.Context, orgID uuid.UUID, title string) (agents.ImprovementItem, bool) {
	items, err := o.Registry.ListImprovements(ctx, orgID, agents.ImprovementFilter{Kind: agents.KindIssue})
	if err != nil {
		return agents.ImprovementItem{}, false
	}
	cutoff := time.Now().Add(-issueDuplicateWindow)
	for _, it := range items {
		if it.CreatedAt.After(cutoff) && strings.EqualFold(strings.TrimSpace(it.Title), title) {
			return it, true
		}
	}
	return agents.ImprovementItem{}, false
}

func issueLinkOr(item agents.ImprovementItem, fallback string) string {
	if strings.TrimSpace(item.Link) != "" {
		return item.Link
	}
	return fallback
}

// issueURL picks the address out of whatever the plugin returned. Three names
// cover the trackers covey speaks to; without one the issue is filed all the
// same, it just carries no link.
func issueURL(res any) string {
	raw, err := json.Marshal(res)
	if err != nil {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	for _, key := range []string{"web_url", "html_url", "url"} {
		if v, ok := m[key].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
