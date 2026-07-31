package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/skills"
)

// Agenten-Skills: die Org-Bibliothek und die agent-eigenen Fähigkeiten.
//
// Nicht zu verwechseln mit skill.go — dort geht es um den mitgelieferten
// Claude-Code-Skill FÜR MENSCHEN (covey-agent.zip, zum Bauen von Agenten). Hier
// geht es um die Fähigkeiten, die ein Agent zur Laufzeit in sein Home
// materialisiert bekommt (internal/skills, spec: Beschreibung immer im Kontext,
// Rumpf nur bei Bedarf).
//
// Zwei Ebenen wie bei den Secrets: Bibliotheks-Skills (agent_id NULL) wirken
// erst nach expliziter Verlinkung, agent-eigene gehören genau einem Agenten.
// Deshalb ist die Bibliothek unter /api/v1/skills erreichbar, das Anlegen eines
// agent-eigenen Skills unter /api/v1/agents/{id}/skills — bearbeitet und
// gelöscht werden beide über ihre ID unter /api/v1/skills/{id}.
//
// Rechte: Lesen darf jede Rolle (wie die Agenten-Config — Skills sind
// Prozeduren, keine Geheimnisse), Ändern nur die Manage-Rollen.

// skillsStore holt den Store und beantwortet den nicht-konfigurierten Fall
// selbst. nil heißt: Feature abgeschaltet (wie bei Options.Skills im
// Orchestrator) — dann ist die API ehrlich unverfügbar statt zu paniken.
func (s *Server) skillsStore(w http.ResponseWriter) (*skills.Store, bool) {
	if s.Skills == nil {
		writeErr(w, http.StatusServiceUnavailable, "skills sind in dieser Instanz nicht konfiguriert")
		return nil, false
	}
	return s.Skills, true
}

// requireAgent löst {id} auf und stellt sicher, dass der Agent zur Organisation
// des Aufrufers gehört. Fremde Organisation = nicht gefunden; ein 403 würde
// verraten, dass es die ID gibt.
func (s *Server) requireAgent(w http.ResponseWriter, r *http.Request) (agents.Agent, bool) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return agents.Agent{}, false
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return agents.Agent{}, false
	}
	if agent.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "agent nicht gefunden")
		return agents.Agent{}, false
	}
	return agent, true
}

// skillInput ist der Request-Body zum Anlegen und Ersetzen.
type skillInput struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Files       []skills.File `json:"files"`
}

// spec macht aus der Eingabe eine Store-Spec.
//
// Trägt die SKILL.md einen YAML-Frontmatter — weil jemand eine fertige Datei
// hineinkopiert oder ein Bundle importiert hat —, wird er abgeschnitten und
// füllt Name und Beschreibung, soweit die Formularfelder leer bleiben. Covey
// hält die Beschreibung als Spalte; läge sie zusätzlich in der Datei, könnten
// sich beide widersprechen und niemand wüsste, welche gilt.
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

// mapSkillErr übersetzt die Store-Fehler. Eingabefehler (ErrInvalid und alles,
// was ihn einwickelt) sind 400 — sie sagen dem Aufrufer, was er falsch gemacht
// hat, und gehören nicht in ein 500.
func mapSkillErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skills.ErrNotFound):
		writeErr(w, http.StatusNotFound, "skill nicht gefunden")
	case errors.Is(err, skills.ErrExists):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, skills.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		mapErr(w, err)
	}
}

// --- Org-Bibliothek ---

// handleListSkills — GET /api/v1/skills. Ohne Dateien; jeder Eintrag trägt in
// assigned_to die verlinkten Agenten, damit die UI zeigen kann, was tatsächlich
// irgendwo wirkt (ein Bibliotheks-Skill ohne Verlinkung erreicht niemanden).
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

// handleCreateSkill — POST /api/v1/skills (Bibliotheks-Skill anlegen).
func (s *Server) handleCreateSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	var in skillInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	sk, err := store.Create(r.Context(), principalFrom(r).OrgID, in.spec(nil))
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}

// handleGetSkill — GET /api/v1/skills/{id}, samt Dateien.
func (s *Server) handleGetSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	full, err := store.Get(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, full)
}

// handlePutSkill — PUT /api/v1/skills/{id}: ersetzt Beschreibung und
// Dateisatz vollständig (weggelassene Dateien sind gelöscht, siehe
// skills.Store.Upsert).
//
// Der Name bleibt unveränderlich: Er ist der Verzeichnisname im Agenten-Home
// und damit der /slash-command, auf den Playbooks und andere Skills verweisen.
// Umbenennen hieße, diese Verweise still ins Leere laufen zu lassen — dafür
// neu anlegen und den alten löschen.
func (s *Server) handlePutSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	spec := in.spec(cur.AgentID)
	if spec.Name != "" && spec.Name != cur.Name {
		writeErr(w, http.StatusConflict,
			"der Name eines Skills ist sein Verzeichnis und sein /slash-command — zum Umbenennen neu anlegen und den alten löschen")
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

// handleDeleteSkill — DELETE /api/v1/skills/{id}. Zuweisungen und Dateien
// gehen per ON DELETE CASCADE mit; die betroffenen Agenten verlieren das
// Verzeichnis beim nächsten Lauf (der Daemon räumt entzogene Skills ab).
func (s *Server) handleDeleteSkill(w http.ResponseWriter, r *http.Request) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := store.Delete(r.Context(), principalFrom(r).OrgID, id); err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Verlinkung Bibliothek → Agent (Muster: secrets/{key}/agents/{agentID}) ---

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

// skillLink parst die beiden IDs beider Verlinkungs-Routen und prüft, dass der
// Agent zur Organisation des Aufrufers gehört — der Fremdschlüssel allein
// verhindert nur unbekannte Agenten, nicht fremde Organisationen.
func (s *Server) skillLink(w http.ResponseWriter, r *http.Request) (*skills.Store, uuid.UUID, uuid.UUID, bool) {
	store, ok := s.skillsStore(w)
	if !ok {
		return nil, uuid.Nil, uuid.Nil, false
	}
	skillID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return nil, uuid.Nil, uuid.Nil, false
	}
	agentID, err := uuid.Parse(r.PathValue("agentID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige agent-id")
		return nil, uuid.Nil, uuid.Nil, false
	}
	agent, err := s.Registry.Get(r.Context(), agentID)
	if err != nil {
		mapErr(w, err)
		return nil, uuid.Nil, uuid.Nil, false
	}
	if agent.OrgID != principalFrom(r).OrgID {
		writeErr(w, http.StatusNotFound, "agent nicht gefunden")
		return nil, uuid.Nil, uuid.Nil, false
	}
	return store, skillID, agentID, true
}

// --- Sicht des Agenten ---

// agentSkill ist ein aufgelöster Skill mit Herkunftsvermerk. Dieselbe
// Unterscheidung trägt später das Bundle (origin: agent|library), damit ein
// Import weiß, was zum Agenten gehört und was aus der Bibliothek kam.
type agentSkill struct {
	skills.Full
	Origin string `json:"origin"`
}

// handleAgentSkills — GET /api/v1/agents/{id}/skills.
//
// Liefert, was der Agent tatsächlich bekommt: eigene plus verlinkte Skills,
// bei Namensgleichheit gewinnt der eigene (skills.ForAgent). Ein verlinkter
// Bibliotheks-Skill, den ein gleichnamiger eigener verdeckt, fehlt hier
// deshalb bewusst — die Verlinkung selbst steht im assigned_to der Bibliothek.
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

// handleCreateAgentSkill — POST /api/v1/agents/{id}/skills: ein Skill, der nur
// diesem Agenten gehört. Bearbeitet und gelöscht wird er danach wie jeder
// andere über /api/v1/skills/{id}.
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
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	sk, err := store.Create(r.Context(), agent.OrgID, in.spec(&agent.ID))
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sk)
}
