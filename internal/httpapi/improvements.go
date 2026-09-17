package httpapi

// The list of open points — and thereby the channel (spec/21).
//
// A review ends in one of three diagnoses, and all three of them need a
// human: the proposal with its diff, the finding without one, and the
// issue that was already submitted. The temptation would be to build a
// way to SAY something to somebody — the platform knows no message to a
// human, and to invent one here would mean putting a second, worse inbox
// beside the one that must exist for the accepting anyway. So the
// acceptance interface is the channel: a finding that only a human can
// edit is then not a message that can get lost, but an open point that
// stays open.
//
// The points are NOT created here. A human who wants to change a config
// changes it — he needs no proposal to himself. The write path belongs
// to the agent (covey/propose_agent_config, a later slice); this file is
// the other end.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/identity"
)

// fileDiff is a changed file as the interface shows it: before the running
// state, after the proposed one. Deliberately against the RUNNING state and
// not against the base of the proposal — what a human judges is the change
// that his clicking creates.
type fileDiff struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// improvementView is the point plus everything else the interface would
// otherwise have to ask for: whom it is about, who wrote it, whether the
// base drifted away and who may accept it.
type improvementView struct {
	agents.ImprovementItem
	AgentSlug    string     `json:"agent_slug"`
	AgentName    string     `json:"agent_name"`
	AgentOwnerID *uuid.UUID `json:"agent_owner_id,omitempty"`
	AuthorSlug   string     `json:"author_slug,omitempty"`
	AuthorName   string     `json:"author_name,omitempty"`
	// CurrentVersion is the version that runs right now. Stale says that the
	// proposal was written against an older one — that alone does not make it
	// wrong, it is a warning.
	CurrentVersion int  `json:"current_version"`
	Stale          bool `json:"stale"`
	// Conflicts are the files that someone else has changed since the base.
	// While the list is not empty, the proposal is not accepted — otherwise
	// the acceptance would silently overwrite a foreign
	// change.
	Conflicts []string `json:"conflicts,omitempty"`
	// NeedsSecurity: the proposal touches ACCESS.md or EGRESS.md. Then it is
	// not the team lead who owns the agent that decides, but org_admin/security
	// (spec/02, spec/21).
	NeedsSecurity bool       `json:"needs_security"`
	Diff          []fileDiff `json:"diff,omitempty"`
}

// improvementRoles may READ the list. Controlling is missing deliberately: a
// cost sheet says what was spent, a proposal says how somebody worked — the
// same boundary that spec/21 draws for the work record.
func improvementReadRoles() []string {
	return []string{identity.RoleOrgAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor}
}

func (s *Server) handleListImprovements(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	f := agents.ImprovementFilter{
		Status: strings.TrimSpace(r.URL.Query().Get("status")),
		Kind:   strings.TrimSpace(r.URL.Query().Get("kind")),
	}
	// "List per agent owner": mine=1 shows only the points about the
	// colleagues that belong to the requester. Filtered server-side and not
	// in the interface — an empty list should arrive empty and not be
	// hidden.
	if r.URL.Query().Get("mine") == "1" {
		owned, err := s.Registry.List(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		ids := []uuid.UUID{}
		for _, a := range owned {
			if a.OwnerID != nil && *a.OwnerID == p.ID {
				ids = append(ids, a.ID)
			}
		}
		f.AgentIDs = ids
	}
	items, err := s.Registry.ListImprovements(r.Context(), p.OrgID, f)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.improvementViews(r, items))
}

func (s *Server) handleGetImprovement(w http.ResponseWriter, r *http.Request) {
	item, ok := s.improvementFromPath(w, r)
	if !ok {
		return
	}
	views := s.improvementViews(r, []agents.ImprovementItem{item})
	writeJSON(w, http.StatusOK, views[0])
}

// improvementFromPath reads the item from the URL and checks the organisation.
// Foreign and not present are the same answer.
func (s *Server) improvementFromPath(w http.ResponseWriter, r *http.Request) (agents.ImprovementItem, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return agents.ImprovementItem{}, false
	}
	item, err := s.Registry.GetImprovement(r.Context(), id)
	if err != nil || item.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "not found")
		return agents.ImprovementItem{}, false
	}
	return item, true
}

// improvementViews enriches the items. The configs are read ONCE per agent, not
// per item: an open list typically holds several
// items about a few colleagues.
func (s *Server) improvementViews(r *http.Request, items []agents.ImprovementItem) []improvementView {
	ctx := r.Context()
	agentCache := map[uuid.UUID]agents.Agent{}
	lookupAgent := func(id uuid.UUID) (agents.Agent, bool) {
		if a, ok := agentCache[id]; ok {
			return a, true
		}
		a, err := s.Registry.Get(ctx, id)
		if err != nil {
			return agents.Agent{}, false
		}
		agentCache[id] = a
		return a, true
	}
	configCache := map[uuid.UUID]agents.ConfigVersion{}
	lookupConfig := func(id uuid.UUID) agents.ConfigVersion {
		if c, ok := configCache[id]; ok {
			return c
		}
		c, err := s.Registry.CurrentConfig(ctx, id)
		if err != nil {
			c = agents.ConfigVersion{Files: map[string]string{}}
		}
		configCache[id] = c
		return c
	}

	out := make([]improvementView, 0, len(items))
	for _, item := range items {
		v := improvementView{ImprovementItem: item}
		if a, ok := lookupAgent(item.AgentID); ok {
			v.AgentSlug, v.AgentName, v.AgentOwnerID = a.Slug, a.DisplayName, a.OwnerID
		}
		if item.AuthorAgentID != nil {
			if a, ok := lookupAgent(*item.AuthorAgentID); ok {
				v.AuthorSlug, v.AuthorName = a.Slug, a.DisplayName
			}
		}
		if item.Kind != agents.KindProposal {
			out = append(out, v)
			continue
		}
		cur := lookupConfig(item.AgentID)
		v.CurrentVersion = cur.Version
		v.NeedsSecurity = len(agents.RestrictedChanges(cur.Files, item.Files)) > 0
		for _, name := range agents.ChangedFiles(cur.Files, item.Files) {
			v.Diff = append(v.Diff, fileDiff{File: name, Before: cur.Files[name], After: item.Files[name]})
		}
		// Outdated and in conflict are questions to an OPEN proposal. For an
		// accepted one they are not only superfluous but wrong:
		// the running version then contains exactly its files, so the
		// comparison reliably reports "changed in the meantime" — and precisely
		// for the files the acceptance itself wrote. In the archive, behind
		// every successful acceptance, a red conflict stood.
		if item.Status == agents.ImprovementPending {
			v.Stale = item.BaseVersion != cur.Version
			if v.Stale {
				v.Conflicts = s.proposalConflicts(r, item, cur)
			}
		}
		out = append(out, v)
	}
	return out
}

// proposalConflicts compares the basis of the proposal with the running
// state. If the base version is no longer there (deleted, or there never was one),
// the empty set counts as the basis — then exactly those files are in conflict that
// already hold content today.
func (s *Server) proposalConflicts(r *http.Request, item agents.ImprovementItem, cur agents.ConfigVersion) []string {
	base := map[string]string{}
	if item.BaseVersion > 0 {
		if bv, err := s.Registry.ConfigAtVersion(r.Context(), item.AgentID, item.BaseVersion); err == nil {
			base = bv.Files
		}
	}
	return agents.ProposalConflicts(base, cur.Files, item.Files)
}

// handleDecideImprovement is the one action of this interface: accept
// or reject, both by a human.
//
// Accept means, for the proposal: merge and save over the NORMAL write path as
// a new version, with the human as author. There is no second
// path into a running config, and therefore no place either where a
// proposal could do something a human could not.
func (s *Server) handleDecideImprovement(w http.ResponseWriter, r *http.Request) {
	item, ok := s.improvementFromPath(w, r)
	if !ok {
		return
	}
	var in struct {
		Accept bool   `json:"accept"`
		Note   string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if item.Status != agents.ImprovementPending {
		writeErr(w, http.StatusConflict, "this item has already been decided")
		return
	}
	p := principalFrom(r)

	// Rejecting costs nothing and takes nothing away: that is allowed to anyone
	// who may operate the list. The reason stays on record — a rejected proposal
	// is the most useful thing someone can read who wants to check
	// covey Doctor themselves.
	if !in.Accept {
		s.finishImprovement(w, r, item.ID, agents.ImprovementRejected, in.Note, 0)
		return
	}
	// Finding and issue have no diff — they are ticked off, not applied.
	if item.Kind != agents.KindProposal {
		s.finishImprovement(w, r, item.ID, agents.ImprovementAccepted, in.Note, 0)
		return
	}

	cur, err := s.Registry.CurrentConfig(r.Context(), item.AgentID)
	if err != nil && !errors.Is(err, agents.ErrNotFound) {
		mapErr(w, err)
		return
	}
	if cur.Files == nil {
		cur.Files = map[string]string{}
	}
	// Conflict before role: a proposal whose basis wandered away does not
	// even reach the role question. It is rewritten or discarded —
	// the same answer a pull request gives to that.
	if conflicts := s.proposalConflicts(r, item, cur); len(conflicts) > 0 {
		writeErr(w, http.StatusConflict,
			"the configuration has changed since this proposal was written ("+
				strings.Join(conflicts, ", ")+") — it has to be rewritten or discarded")
		return
	}
	// The role boundary from spec/02, inherited rather than bypassed: whoever changes
	// ACCESS.md or EGRESS.md widens a colleague's access. That is not decided
	// by who clicked first.
	if restricted := agents.RestrictedChanges(cur.Files, item.Files); len(restricted) > 0 {
		if p.Role != identity.RoleOrgAdmin && p.Role != identity.RoleSecurity {
			writeErr(w, http.StatusForbidden,
				"this proposal changes "+strings.Join(restricted, " and ")+
					" — only org_admin or security may accept it")
			return
		}
	}

	merged := agents.MergeConfig(cur.Files, item.Files)
	// The write-through may touch ONLY what the proposal really
	// changes.
	//
	// ACCESS.md and EGRESS.md do stand in the snapshot, but are not
	// authoritative there: the tools tab and the egress routes change the
	// assignment without writing a config version. If the snapshot went into
	// prepareConfigWrite as is, its apply() would call SetAgentTools with the
	// stale list — and accepting a proposal about PLAYBOOKS.md would lift
	// the tool restriction someone set through the interface.
	// For an agent_owner the same cause would be a 403 on a
	// proposal that does not touch access at all.
	//
	// prepareConfigApply leaves an area alone when its file is missing
	// ("omitted EGRESS.md means no change"). So it is missing there — saved
	// the version is complete all the same, so the snapshot does not get a
	// gap.
	writeThrough := make(map[string]string, len(merged))
	for k, v := range merged {
		writeThrough[k] = v
	}
	for _, name := range []string{"ACCESS.md", "EGRESS.md"} {
		if _, touched := item.Files[name]; !touched {
			delete(writeThrough, name)
		}
	}
	apply, ok := s.prepareConfigWrite(w, r, item.AgentID, writeThrough)
	if !ok {
		return
	}
	cv, err := s.Registry.SaveConfig(r.Context(), item.AgentID, merged, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := apply(r.Context()); err != nil {
		s.Log.Error("improvement: config write-through", "agent", item.AgentID, "err", err)
		writeErr(w, http.StatusInternalServerError,
			"version saved, but applying it to tools/egress failed: "+err.Error())
		return
	}
	s.finishImprovement(w, r, item.ID, agents.ImprovementAccepted, in.Note, cv.Version)
}

// finishImprovement records the decision. The UPDATE runs against
// "pending"; if two people click at the same time, one wins and
// the other gets 409 instead of a second decision.
func (s *Server) finishImprovement(w http.ResponseWriter, r *http.Request, id uuid.UUID,
	status, note string, appliedVersion int) {

	p := principalFrom(r)
	decided, err := s.Registry.DecideImprovement(r.Context(), id, status, p.ID, strings.TrimSpace(note), appliedVersion)
	if errors.Is(err, agents.ErrNotPending) {
		writeErr(w, http.StatusConflict, "this item has already been decided")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	views := s.improvementViews(r, []agents.ImprovementItem{decided})
	writeJSON(w, http.StatusOK, views[0])
}
