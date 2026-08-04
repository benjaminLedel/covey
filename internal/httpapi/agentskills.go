package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/skills"
)

// Agent skills: the org library and the agent-owned capabilities.
//
// Not to be confused with skill.go — that one is about the bundled Claude Code
// skill FOR HUMANS (covey-agent.zip, for building agents). Here it is about
// the capabilities an agent gets materialized into its home at runtime
// (internal/skills, spec: description always in context, body only on demand).
//
// Two levels as with the secrets: library skills (agent_id NULL) only take
// effect after being linked explicitly, agent-owned ones belong to exactly one
// agent. That is why the library is reachable under /api/v1/skills and
// creating an agent-owned skill under /api/v1/agents/{id}/skills — both are
// edited and deleted by their ID under /api/v1/skills/{id}.
//
// Permissions: every role may read (like the agent config — skills are
// procedures, not secrets), only the manage roles may change.

// skillsStore fetches the store and answers the not-configured case itself.
// nil means: feature switched off (as with Options.Skills in the
// orchestrator) — then the API is honestly unavailable instead of panicking.
func (s *Server) skillsStore(w http.ResponseWriter) (*skills.Store, bool) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills are not configured on this instance")
		return nil, false
	}
	return s.Skills, true
}

// requireAgent resolves {id} and makes sure the agent belongs to the caller's
// organization. Foreign organization = not found; a 403 would give away that
// the ID exists.
func (s *Server) requireAgent(w http.ResponseWriter, r *http.Request) (agents.Agent, bool) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return agents.Agent{}, false
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return agents.Agent{}, false
	}
	if agent.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "agent not found")
		return agents.Agent{}, false
	}
	return agent, true
}

// skillInput is the request body for creating and replacing.
type skillInput struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Files       []skills.File `json:"files"`
}

// checked is spec() with the frontmatter check in front of it: a key Covey
// does not keep is rejected instead of being dropped on save (see
// skills.UnsupportedFrontmatterKeys). All write paths go through here.
func (in skillInput) checked(agentID *uuid.UUID) (skills.Spec, error) {
	for _, f := range in.Files {
		if f.Path != skills.EntryFile {
			continue
		}
		if keys := skills.UnsupportedFrontmatterKeys(f.Content); len(keys) > 0 {
			return skills.Spec{}, fmt.Errorf(
				"%w: %s frontmatter carries %s — Covey only stores name and description, everything else would be lost on save",
				skills.ErrInvalid, skills.EntryFile, strings.Join(keys, ", "))
		}
	}
	return in.spec(agentID), nil
}

// spec turns the input into a store spec.
//
// If the SKILL.md carries a YAML frontmatter — because someone pasted a
// finished file or imported a bundle — it is cut off and fills in name and
// description as far as the form fields stay empty. Covey keeps the
// description as a column; were it in the file as well, the two could
// contradict each other and nobody would know which one applies.
func (in skillInput) spec(agentID *uuid.UUID) skills.Spec {
	spec := skills.Spec{
		Name:        in.Name,
		Description: in.Description,
		AgentID:     agentID,
		Files:       make([]skills.File, 0, len(in.Files)),
	}
	for _, f := range in.Files {
		if f.Path == skills.EntryFile {
			name, desc, body := skills.SplitEntry(f.Content)
			if spec.Name == "" {
				spec.Name = name
			}
			if spec.Description == "" {
				spec.Description = desc
			}
			f.Content = body
		}
		spec.Files = append(spec.Files, f)
	}
	return spec
}

// mapSkillErr translates the store errors. Input errors (ErrInvalid and
// everything wrapping it) are 400 — they tell the caller what he did wrong and
// do not belong in a 500.
func mapSkillErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skills.ErrNotFound):
		writeErr(w, http.StatusNotFound, "skill not found")
	case errors.Is(err, skills.ErrExists):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, skills.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		mapErr(w, err)
	}
}

// --- Org library ---

// handleListSkills — GET /api/v1/skills. Without files; every entry carries
// the linked agents in assigned_to so the UI can show what actually takes
// effect somewhere (a library skill without a link reaches nobody).
func (s *Server) handleListSkills(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	list, err := store.ListLibrary(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCreateSkill — POST /api/v1/skills (create a library skill).
func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	var in skillInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	spec, err := in.checked(nil)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	sk, err := store.Create(r.Context(), principalFrom(r).OrgID, spec)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}

// handleGetSkill — GET /api/v1/skills/{id}, files included.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	full, err := store.Get(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, full)
}

// handlePutSkill — PUT /api/v1/skills/{id}: replaces description and file set
// completely (omitted files are deleted, see skills.Store.Upsert).
//
// The name stays immutable: it is the directory name in the agent's home and
// therefore the /slash-command that playbooks and other skills refer to.
// Renaming would mean letting those references quietly run into the void —
// create a new one for that and delete the old.
func (s *Server) handlePutSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := principalFrom(r).OrgID
	cur, err := store.Get(r.Context(), orgID, id)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	var in skillInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	spec, err := in.checked(cur.AgentID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if spec.Name != "" && spec.Name != cur.Name {
		writeErr(w, http.StatusConflict,
			"the name of a skill is its directory and its /slash-command — to rename it, create a new one and delete the old")
		return
	}
	spec.Name = cur.Name
	sk, err := store.Upsert(r.Context(), orgID, spec)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sk)
}

// handleDeleteSkill — DELETE /api/v1/skills/{id}. Assignments and files go
// along via ON DELETE CASCADE; the affected agents lose the directory on their
// next run (the daemon cleans up revoked skills).
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := store.Delete(r.Context(), principalFrom(r).OrgID, id); err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Linking library → agent (pattern: secrets/{key}/agents/{agentID}) ---

// handleAssignSkill — PUT /api/v1/skills/{id}/agents/{agentID}.
func (s *Server) handleAssignSkill(w http.ResponseWriter, r *http.Request) {
	store, skillID, agentID, ok := s.skillLink(w, r)
	if !ok {
		return
	}
	if err := store.Assign(r.Context(), principalFrom(r).OrgID, skillID, agentID); err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleUnassignSkill — DELETE /api/v1/skills/{id}/agents/{agentID}.
func (s *Server) handleUnassignSkill(w http.ResponseWriter, r *http.Request) {
	store, skillID, agentID, ok := s.skillLink(w, r)
	if !ok {
		return
	}
	if err := store.Unassign(r.Context(), principalFrom(r).OrgID, skillID, agentID); err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// skillLink parses the two IDs of both linking routes and checks that the
// agent belongs to the caller's organization — the foreign key alone only
// prevents unknown agents, not foreign organizations.
func (s *Server) skillLink(w http.ResponseWriter, r *http.Request) (*skills.Store, uuid.UUID, uuid.UUID, bool) {
	store, ok := s.skillsStore(w)
	if !ok {
		return nil, uuid.Nil, uuid.Nil, false
	}
	skillID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return nil, uuid.Nil, uuid.Nil, false
	}
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return nil, uuid.Nil, uuid.Nil, false
	}
	agent, err := s.Registry.Get(r.Context(), agentID)
	if err != nil {
		mapErr(w, err)
		return nil, uuid.Nil, uuid.Nil, false
	}
	if agent.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "agent not found")
		return nil, uuid.Nil, uuid.Nil, false
	}
	return store, skillID, agentID, true
}

// --- The agent's view ---

// agentSkill is a resolved skill with a note on its origin. The bundle carries
// the same distinction later on (origin: agent|library) so that an import
// knows what belongs to the agent and what came from the library.
type agentSkill struct {
	skills.Full
	Origin string `json:"origin"`
}

// handleAgentSkills — GET /api/v1/agents/{id}/skills.
//
// Returns what the agent actually gets: own plus linked skills, on a name
// clash its own wins (skills.ForAgent). A linked library skill shadowed by an
// own one of the same name is therefore deliberately missing here — the link
// itself shows up in the library's assigned_to.
func (s *Server) handleAgentSkills(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	found, err := store.ForAgent(r.Context(), agent.OrgID, agent.ID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	out := make([]agentSkill, 0, len(found))
	for _, sk := range found {
		origin := "library"
		if !sk.Library() {
			origin = "agent"
		}
		out = append(out, agentSkill{Full: sk, Origin: origin})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateAgentSkill — POST /api/v1/agents/{id}/skills: a skill that
// belongs to this agent alone. Afterwards it is edited and deleted like every
// other one via /api/v1/skills/{id}.
func (s *Server) handleCreateAgentSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	var in skillInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	spec, err := in.checked(&agent.ID)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	sk, err := store.Create(r.Context(), agent.OrgID, spec)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}
