package httpapi

// Voices per occasion, chosen by who is spoken to (#471).
//
// Three holders name a voice per occasion (chat, customers, publications):
// an agent, a department, the organisation. The endpoints here set and read
// those slots, and answer the two questions the voices page asks from the
// other side — "who uses this voice for what" (used_by on the voice) and
// "who gets what" (every department × occasion, resolved).
//
// Permissions as for every other change to a voice or an agent: every role
// reads, the manage roles change.

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/org"
	"covey/internal/voice"
)

// slotRef is one filled slot as the API shows it; null is an empty one.
type slotRef struct {
	VoiceID uuid.UUID `json:"voice_id"`
	Voice   string    `json:"voice"`
}

// parseSlots reads {"chat": "<uuid>", "customers": "", "publications": null}.
// "" and null are an empty slot; an unknown occasion or a malformed id is the
// caller's mistake.
func parseSlots(in map[string]*string) (voice.Slots, error) {
	out := voice.Slots{}
	for k, v := range in {
		occ, err := voice.ParseOccasion(k)
		if err != nil {
			return nil, err
		}
		if v == nil || strings.TrimSpace(*v) == "" {
			out[occ] = uuid.Nil
			continue
		}
		id, err := uuid.Parse(strings.TrimSpace(*v))
		if err != nil {
			return nil, errors.New("invalid voice id for " + k)
		}
		out[occ] = id
	}
	return out, nil
}

// slotsView names the voices of a set of slots, every occasion present.
func (s *Server) slotsView(r *http.Request, slots voice.Slots) map[voice.Occasion]*slotRef {
	out := map[voice.Occasion]*slotRef{}
	for _, occ := range voice.Occasions {
		out[occ] = nil
		id := slots[occ]
		if id == uuid.Nil {
			continue
		}
		ref := &slotRef{VoiceID: id}
		if v, err := s.Voices.Get(r.Context(), principalFrom(r).OrgID, id); err == nil {
			ref.Voice = v.Name
		}
		out[occ] = ref
	}
	return out
}

// writeSlotErr answers a refused change of slots.
func writeSlotErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, voice.ErrNotFound):
		writeErr(w, http.StatusBadRequest, "no such voice in this organisation")
	default:
		mapErr(w, err)
	}
}

// agentSlotsView is GET/PUT /agents/{id}/voices: the agent's own slots, and
// what each occasion resolves to when nothing is known about whom it writes
// to — its own slot, else the organisation's default.
type agentSlotsView struct {
	Slots     map[voice.Occasion]*slotRef   `json:"slots"`
	Effective map[voice.Occasion]voice.Cell `json:"effective"`
}

func (s *Server) agentSlots(w http.ResponseWriter, r *http.Request, agentID uuid.UUID) {
	orgID := principalFrom(r).OrgID
	slots, err := s.Voices.AgentSlots(r.Context(), orgID, agentID)
	if err != nil {
		mapErr(w, err)
		return
	}
	orgSlots, err := s.Voices.OrgSlots(r.Context(), orgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	names := s.slotsView(r, orgSlots)
	for occ, ref := range s.slotsView(r, slots) {
		if ref != nil {
			names[occ] = ref
		}
	}
	eff := map[voice.Occasion]voice.Cell{}
	for _, occ := range voice.Occasions {
		c := voice.Choose(occ, voice.Audience{}, slots, orgSlots)
		cell := voice.Cell{Level: c.Level, Reason: c.Reason()}
		if c.Found() {
			id := c.VoiceID
			cell.VoiceID = &id
			if ref := names[occ]; ref != nil {
				cell.Voice = ref.Voice
			}
		}
		eff[occ] = cell
	}
	writeJSON(w, http.StatusOK, agentSlotsView{Slots: s.slotsView(r, slots), Effective: eff})
}

func (s *Server) handleGetAgentVoices(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.voiceStore(w); !ok {
		return
	}
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	s.agentSlots(w, r, agent.ID)
}

// handleSetAgentVoices replaces the agent's three slots: an occasion missing
// from the body is empty afterwards. Nothing is written into the agent's
// config — the voice is resolved when the agent writes (#471), and a
// department's voice can take the place of the agent's there.
func (s *Server) handleSetAgentVoices(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	var in map[string]*string
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	slots, err := parseSlots(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.SetAgentSlots(r.Context(), agent.OrgID, agent.ID, slots); err != nil {
		writeSlotErr(w, err)
		return
	}
	s.agentSlots(w, r, agent.ID)
}

// handleGetOrgVoices: the organisation's default voice per occasion.
func (s *Server) handleGetOrgVoices(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	slots, err := store.OrgSlots(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slots": s.slotsView(r, slots)})
}

// handleSetOrgVoices changes the defaults named in the body and leaves the
// others; "" or null empties one.
func (s *Server) handleSetOrgVoices(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	var in map[string]*string
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	slots, err := parseSlots(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	orgID := principalFrom(r).OrgID
	if err := store.SetOrgSlots(r.Context(), orgID, slots); err != nil {
		writeSlotErr(w, err)
		return
	}
	s.handleGetOrgVoices(w, r)
}

// handleSetDepartmentAudience: how a department wants to be spoken to — one
// line, at most org.AudienceNoteMax characters.
func (s *Server) handleSetDepartmentAudience(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		AudienceNote string `json:"audience_note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	note, err := s.Org.SetDepartmentAudience(r.Context(), principalFrom(r).OrgID, id, in.AudienceNote)
	if errors.Is(err, org.ErrAudienceNoteTooLong) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"audience_note": note})
}

// handleSetDepartmentVoices replaces the department's voices per occasion,
// like the agent's: a missing occasion is empty afterwards.
func (s *Server) handleSetDepartmentVoices(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	orgID := principalFrom(r).OrgID
	if _, err := s.Org.GetDepartment(r.Context(), orgID, id); err != nil {
		mapErr(w, err)
		return
	}
	var in map[string]*string
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	slots, err := parseSlots(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := store.SetDepartmentSlots(r.Context(), orgID, id, slots); err != nil {
		writeSlotErr(w, err)
		return
	}
	now, err := store.DepartmentSlots(r.Context(), orgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"voices": s.slotsView(r, now)})
}

// handleVoiceAssignments is "who gets what": for every department and for
// nobody's department, the voice each occasion resolves to and why.
// ?agent_id= counts that agent's slots as level 2; without it the table
// shows what departments and organisation say on their own.
func (s *Server) handleVoiceAssignments(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	p := principalFrom(r)
	agentID := uuid.Nil
	if raw := r.URL.Query().Get("agent_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent_id")
			return
		}
		a, err := s.Registry.Get(r.Context(), id)
		if err != nil || a.OrgID != p.OrgID {
			writeErr(w, http.StatusNotFound, "agent not found")
			return
		}
		agentID = id
	}
	rows, err := store.WhoGetsWhat(r.Context(), p.OrgID, agentID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"occasions": voice.Occasions, "rows": rows})
}
