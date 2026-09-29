package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/* The slots of #471 in the database (voice_assignments, 0122): which voice an
 * agent, a department or the organisation names per occasion, and the reads
 * that turn a conversation or a task into the Audience the rule in
 * occasion.go chooses from. */

// holder is the column a slot row hangs off; "" is the organisation's default.
type holder string

const (
	holderAgent      holder = "agent_id"
	holderDepartment holder = "department_id"
	holderOrg        holder = ""
)

func (h holder) where() string {
	switch h {
	case holderAgent:
		return "agent_id = $2"
	case holderDepartment:
		return "department_id = $2"
	}
	return "agent_id IS NULL AND department_id IS NULL AND $2::uuid IS NULL"
}

// AgentSlots are the voices an agent names per occasion.
func (s *Store) AgentSlots(ctx context.Context, orgID, agentID uuid.UUID) (Slots, error) {
	return s.slots(ctx, orgID, holderAgent, &agentID)
}

// OrgSlots are the organisation's defaults per occasion.
func (s *Store) OrgSlots(ctx context.Context, orgID uuid.UUID) (Slots, error) {
	return s.slots(ctx, orgID, holderOrg, nil)
}

// DepartmentSlots are the voices one department names per occasion.
func (s *Store) DepartmentSlots(ctx context.Context, orgID, deptID uuid.UUID) (Slots, error) {
	return s.slots(ctx, orgID, holderDepartment, &deptID)
}

// AllDepartmentSlots are the slots of every department of the organisation
// that names at least one voice.
func (s *Store) AllDepartmentSlots(ctx context.Context, orgID uuid.UUID) (map[uuid.UUID]Slots, error) {
	rows, err := s.pool.Query(ctx, `SELECT department_id, occasion, voice_id FROM voice_assignments
		WHERE org_id = $1 AND department_id IS NOT NULL`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]Slots{}
	for rows.Next() {
		var dept, voiceID uuid.UUID
		var occ string
		if err := rows.Scan(&dept, &occ, &voiceID); err != nil {
			return nil, err
		}
		if out[dept] == nil {
			out[dept] = Slots{}
		}
		out[dept][Occasion(occ)] = voiceID
	}
	return out, rows.Err()
}

func (s *Store) slots(ctx context.Context, orgID uuid.UUID, h holder, id *uuid.UUID) (Slots, error) {
	rows, err := s.pool.Query(ctx, `SELECT occasion, voice_id FROM voice_assignments
		WHERE org_id = $1 AND `+h.where(), orgID, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := Slots{}
	for rows.Next() {
		var occ string
		var voiceID uuid.UUID
		if err := rows.Scan(&occ, &voiceID); err != nil {
			return nil, err
		}
		out[Occasion(occ)] = voiceID
	}
	return out, rows.Err()
}

// SetAgentSlots replaces an agent's slots: an occasion missing from the map
// is empty afterwards. The agent has to be of the organisation — the caller
// resolved it there.
func (s *Store) SetAgentSlots(ctx context.Context, orgID, agentID uuid.UUID, slots Slots) error {
	return s.setSlots(ctx, orgID, holderAgent, &agentID, slots, true)
}

// SetDepartmentSlots replaces a department's slots, as SetAgentSlots.
func (s *Store) SetDepartmentSlots(ctx context.Context, orgID, deptID uuid.UUID, slots Slots) error {
	return s.setSlots(ctx, orgID, holderDepartment, &deptID, slots, true)
}

// SetOrgSlots changes the organisation's defaults for the occasions in the
// map and leaves the others: uuid.Nil empties one.
func (s *Store) SetOrgSlots(ctx context.Context, orgID uuid.UUID, slots Slots) error {
	return s.setSlots(ctx, orgID, holderOrg, nil, slots, false)
}

// setSlots writes slots in one transaction. Every voice named has to be of
// the organisation and assignable — the same condition the one-voice
// assignment has always had: a slot acts in prompts, and an unbuilt or
// unreleased voice has nothing that may act.
func (s *Store) setSlots(ctx context.Context, orgID uuid.UUID, h holder, id *uuid.UUID, slots Slots, replace bool) error {
	for occ, voiceID := range slots {
		if _, err := ParseOccasion(string(occ)); err != nil {
			return err
		}
		if voiceID == uuid.Nil {
			continue
		}
		v, err := s.Get(ctx, orgID, voiceID)
		if err != nil {
			return err
		}
		if !v.Assignable() {
			return fmt.Errorf("%w: the voice %q is not built or not released yet — nothing of it may act", ErrInvalid, v.Name)
		}
	}
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		for _, occ := range Occasions {
			voiceID, named := slots[occ]
			if !named && !replace {
				continue
			}
			if _, err := tx.Exec(ctx, `DELETE FROM voice_assignments WHERE org_id = $1 AND `+h.where()+` AND occasion = $3`,
				orgID, id, string(occ)); err != nil {
				return err
			}
			if voiceID == uuid.Nil {
				continue
			}
			var agentID, deptID *uuid.UUID
			switch h {
			case holderAgent:
				agentID = id
			case holderDepartment:
				deptID = id
			}
			if _, err := tx.Exec(ctx, `INSERT INTO voice_assignments (org_id, agent_id, department_id, occasion, voice_id)
				VALUES ($1, $2, $3, $4, $5)`, orgID, agentID, deptID, string(occ), voiceID); err != nil {
				return err
			}
		}
		return nil
	})
}

// Resolve is the voice an agent writes in for an occasion and an audience:
// voice.Choose with the agent's and the organisation's slots from the table.
// Unreadable slots are empty ones — a text is never held up by the lookup of
// its voice, it is written without one.
func (s *Store) Resolve(ctx context.Context, orgID, agentID uuid.UUID, occ Occasion, aud Audience) Choice {
	agent, _ := s.AgentSlots(ctx, orgID, agentID)
	org, _ := s.OrgSlots(ctx, orgID)
	return Choose(occ, aud, agent, org)
}

// AudienceOf is the Audience of an addressed person and the others involved,
// by their human ids: each one's department, with its line and its slots.
// uuid.Nil for addressed is nobody known — a customer, or a message from
// outside the organisation. People without a department add nothing.
func (s *Store) AudienceOf(ctx context.Context, orgID, addressed uuid.UUID, others []uuid.UUID) Audience {
	ids := append([]uuid.UUID{addressed}, others...)
	rows, err := s.pool.Query(ctx, `SELECT h.id, d.id, d.name, d.audience_note
		  FROM humans h JOIN departments d ON d.id = h.department_id
		 WHERE h.org_id = $1 AND h.id = ANY($2)`, orgID, ids)
	if err != nil {
		return Audience{}
	}
	byHuman := map[uuid.UUID]AudienceDepartment{}
	for rows.Next() {
		var human uuid.UUID
		var d AudienceDepartment
		if rows.Scan(&human, &d.ID, &d.Name, &d.Note) == nil {
			byHuman[human] = d
		}
	}
	rows.Close()
	slots, _ := s.AllDepartmentSlots(ctx, orgID)
	with := func(d AudienceDepartment) AudienceDepartment {
		d.Slots = slots[d.ID]
		return d
	}
	var aud Audience
	if d, ok := byHuman[addressed]; ok && addressed != uuid.Nil {
		d = with(d)
		aud.Addressed = &d
	}
	for _, h := range others {
		if d, ok := byHuman[h]; ok {
			aud.Others = append(aud.Others, with(d))
		}
	}
	return aud
}

// audienceRecent: how many of a conversation's last messages decide who else
// is involved. The same end of the conversation a turn reads, a little less:
// who spoke an hour ago in a busy group is not who is reading now.
const audienceRecent = 8

// ConversationAudience is the Audience of a message in a conversation: the
// person who wrote it (by address) and the people who wrote among its last
// messages. A read of conversation_messages from here rather than the chat
// store because the run's dispatch needs the same answer and has no chat
// store — one implementation for the triage, the narration and the run.
func (s *Store) ConversationAudience(ctx context.Context, orgID, convID uuid.UUID, addressedEmail string) Audience {
	addressed := s.HumanByEmail(ctx, orgID, addressedEmail)
	rows, err := s.pool.Query(ctx, `SELECT author_id FROM (
		SELECT author_id, created_at FROM conversation_messages
		 WHERE conversation_id = $1 AND org_id = $2 AND author_kind = 'human' AND author_id IS NOT NULL
		 ORDER BY created_at DESC LIMIT $3) recent ORDER BY created_at DESC`, convID, orgID, audienceRecent)
	if err != nil {
		return s.AudienceOf(ctx, orgID, addressed, nil)
	}
	var others []uuid.UUID
	seen := map[uuid.UUID]bool{addressed: true}
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil && !seen[id] {
			seen[id] = true
			others = append(others, id)
		}
	}
	rows.Close()
	return s.AudienceOf(ctx, orgID, addressed, others)
}

// HumanByEmail is the id of the organisation's person behind an address;
// uuid.Nil when there is none.
func (s *Store) HumanByEmail(ctx context.Context, orgID uuid.UUID, email string) uuid.UUID {
	email = strings.TrimSpace(email)
	if email == "" {
		return uuid.Nil
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM humans WHERE org_id = $1 AND lower(email) = lower($2)`,
		orgID, email).Scan(&id); err != nil {
		return uuid.Nil
	}
	return id
}

// DepartmentAudience is the Audience of somebody from one department — the
// preview's "how would it sound to Sales".
func (s *Store) DepartmentAudience(ctx context.Context, orgID, deptID uuid.UUID) (Audience, error) {
	d := AudienceDepartment{ID: deptID}
	if err := s.pool.QueryRow(ctx, `SELECT name, audience_note FROM departments WHERE org_id = $1 AND id = $2`,
		orgID, deptID).Scan(&d.Name, &d.Note); err != nil {
		if err == pgx.ErrNoRows {
			return Audience{}, fmt.Errorf("%w: no such department", ErrInvalid)
		}
		return Audience{}, err
	}
	d.Slots, _ = s.DepartmentSlots(ctx, orgID, deptID)
	return Audience{Addressed: &d}, nil
}

// ChatToneFor is the tone of the team chat that goes with a chosen chat voice:
// that voice's, field by field over the organisation's (EffectiveChatTone).
// With no chat voice chosen it is the tone of the agent's customers voice —
// the one voice the agent carried before #471, whose chat tone acted in the
// chat and keeps doing so until somebody names a chat voice.
func (s *Store) ChatToneFor(ctx context.Context, orgID, agentID uuid.UUID, c Choice) ChatTone {
	voiceID := c.VoiceID
	if !c.Found() {
		if slots, err := s.AgentSlots(ctx, orgID, agentID); err == nil {
			voiceID = slots[OccasionCustomers]
		}
	}
	var eigen, org []byte
	if err := s.pool.QueryRow(ctx, `SELECT coalesce((SELECT chat_tone FROM voices WHERE id = $2 AND org_id = $1), '{}'::jsonb),
		o.chat_tone FROM organizations o WHERE o.id = $1`, orgID, voiceID).Scan(&eigen, &org); err != nil {
		return ChatTone{}
	}
	var v, o ChatTone
	_ = json.Unmarshal(eigen, &v)
	_ = json.Unmarshal(org, &o)
	return EffectiveChatTone(v, o)
}

// Use is one place a voice is named: an agent's, a department's or the
// organisation's slot for an occasion.
type Use struct {
	// Holder is "agent", "department" or "org".
	Holder   string     `json:"holder"`
	ID       *uuid.UUID `json:"id,omitempty"`
	Slug     string     `json:"slug,omitempty"`
	Name     string     `json:"name"`
	Occasion Occasion   `json:"occasion"`
}

// UsedBy lists where a voice is named, agents first, then departments, then
// the organisation's defaults; within each by name and occasion.
func (s *Store) UsedBy(ctx context.Context, orgID, voiceID uuid.UUID) ([]Use, error) {
	rows, err := s.pool.Query(ctx, `SELECT
		  CASE WHEN va.agent_id IS NOT NULL THEN 'agent' WHEN va.department_id IS NOT NULL THEN 'department' ELSE 'org' END,
		  coalesce(va.agent_id, va.department_id), coalesce(a.slug, ''), coalesce(a.display_name, d.name, ''), va.occasion
		  FROM voice_assignments va
		  LEFT JOIN agents a ON a.id = va.agent_id
		  LEFT JOIN departments d ON d.id = va.department_id
		 WHERE va.org_id = $1 AND va.voice_id = $2 AND (a.id IS NULL OR NOT a.killed)`, orgID, voiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Use{}
	for rows.Next() {
		var u Use
		var occ string
		if err := rows.Scan(&u.Holder, &u.ID, &u.Slug, &u.Name, &occ); err != nil {
			return nil, err
		}
		u.Occasion = Occasion(occ)
		out = append(out, u)
	}
	rank := map[string]int{"agent": 0, "department": 1, "org": 2}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.Holder] != rank[b.Holder] {
			return rank[a.Holder] < rank[b.Holder]
		}
		if a.Name != b.Name {
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		}
		return occasionRank(a.Occasion) < occasionRank(b.Occasion)
	})
	return out, rows.Err()
}

func occasionRank(o Occasion) int {
	for i, known := range Occasions {
		if o == known {
			return i
		}
	}
	return len(Occasions)
}

// Cell is one voice in effect in the "who gets what" table.
type Cell struct {
	VoiceID *uuid.UUID `json:"voice_id"`
	Voice   string     `json:"voice"`
	Level   Level      `json:"level"`
	Reason  string     `json:"reason"`
}

// Row is one department of the table; Department nil is everybody without
// one — customers, and people the org chart has not placed.
type Row struct {
	Department *DepartmentRef    `json:"department"`
	Note       string            `json:"audience_note"`
	Cells      map[Occasion]Cell `json:"cells"`
}

// DepartmentRef names a department.
type DepartmentRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// WhoGetsWhat is the table of #471: for every department, and for nobody's
// department, the voice each occasion resolves to. agentID names the agent
// whose level 2 counts; uuid.Nil leaves level 2 out, and the table shows
// what the departments and the organisation say on their own.
func (s *Store) WhoGetsWhat(ctx context.Context, orgID, agentID uuid.UUID) ([]Row, error) {
	names := map[uuid.UUID]string{}
	vrows, err := s.pool.Query(ctx, `SELECT id, name FROM voices WHERE org_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var id uuid.UUID
		var name string
		if vrows.Scan(&id, &name) == nil {
			names[id] = name
		}
	}
	vrows.Close()

	depts, err := s.pool.Query(ctx, `SELECT id, name, audience_note FROM departments WHERE org_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	var list []AudienceDepartment
	for depts.Next() {
		var d AudienceDepartment
		if err := depts.Scan(&d.ID, &d.Name, &d.Note); err != nil {
			depts.Close()
			return nil, err
		}
		list = append(list, d)
	}
	depts.Close()
	slots, err := s.AllDepartmentSlots(ctx, orgID)
	if err != nil {
		return nil, err
	}
	agent := Slots{}
	if agentID != uuid.Nil {
		if agent, err = s.AgentSlots(ctx, orgID, agentID); err != nil {
			return nil, err
		}
	}
	org, err := s.OrgSlots(ctx, orgID)
	if err != nil {
		return nil, err
	}
	row := func(aud Audience) map[Occasion]Cell {
		cells := map[Occasion]Cell{}
		for _, occ := range Occasions {
			c := Choose(occ, aud, agent, org)
			cell := Cell{Level: c.Level, Reason: c.Reason()}
			if c.Found() {
				id := c.VoiceID
				cell.VoiceID, cell.Voice = &id, names[id]
			}
			cells[occ] = cell
		}
		return cells
	}
	out := make([]Row, 0, len(list)+1)
	for _, d := range list {
		d.Slots = slots[d.ID]
		dd := d
		out = append(out, Row{Department: &DepartmentRef{ID: d.ID, Name: d.Name}, Note: d.Note,
			Cells: row(Audience{Addressed: &dd})})
	}
	out = append(out, Row{Cells: row(Audience{})})
	return out, nil
}
