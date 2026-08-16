package observability

import (
	"context"

	"github.com/google/uuid"
)

// wirksameFrist is the retention that applies to one agent, as SQL: the
// organisation's value, extended by the agent's own if it has one.
//
// The direction is deliberate and is the whole rule. An agent may keep its
// transcript LONGER than the organisation requires, never shorter — the same
// direction as the recording depth, and for the same reason: an agent that
// could shorten its own trail is precisely the gap an org-wide setting exists
// to close.
//
// 0 means forever, on either side, and forever wins over any number of days —
// which is why this cannot simply be GREATEST(). Written out as a CASE because
// the version with arithmetic tricks was the kind of clever that gets read
// wrong once and deletes an audit trail.
const wirksameFrist = `CASE
		WHEN o.recording_retention_days = 0 THEN NULL
		WHEN a.recording_retention_days = 0 THEN NULL
		WHEN a.recording_retention_days IS NULL THEN o.recording_retention_days
		ELSE GREATEST(o.recording_retention_days, a.recording_retention_days)
	END`

// CleanupRecordings removes the verbatim runs whose retention has run out and
// returns how many went (spec/06, "How long the verbatim record is kept").
//
// ONLY kind='runtime'. What an action, an approval, a credential request or a
// lifecycle change recorded stays: that is the audit trail and the basis of
// every indicator, a KPI defined today still counts backwards over the whole
// history (spec/17), and it is a fraction of the volume anyway — 8 of 384 MB on
// the installation this was measured on. What goes is the transcript
// underneath, which is the other 376.
//
// The kind is written here as a literal rather than taken as an argument. A
// caller that could pass 'action' would be a caller that can delete the
// indicators, and there is no reason for that call to exist.
func (s *Store) CleanupRecordings(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM recording_events r
		 USING agents a JOIN organizations o ON o.id = a.org_id
		 WHERE r.agent_id = a.id
		   AND r.kind = 'runtime'
		   AND `+wirksameFrist+` IS NOT NULL
		   AND r.created_at < now() - make_interval(days => `+wirksameFrist+`)`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// RecordingRetention is what an organisation keeps, and what a single agent
// deviates to. Days; 0 = forever, and for the agent nil = inherit.
type RecordingRetention struct {
	OrgDays   int  `json:"org_days"`
	AgentDays *int `json:"agent_days,omitempty"`
}

// OrgRecordingRetention reads the organisation's value.
func (s *Store) OrgRecordingRetention(ctx context.Context, orgID uuid.UUID) (int, error) {
	var tage int
	err := s.pool.QueryRow(ctx,
		`SELECT recording_retention_days FROM organizations WHERE id = $1`, orgID).Scan(&tage)
	return tage, err
}

// SetOrgRecordingRetention writes it. Negative is not an error worth its own
// message — it is the same statement as 0, and both mean "do not expire".
func (s *Store) SetOrgRecordingRetention(ctx context.Context, orgID uuid.UUID, tage int) error {
	if tage < 0 {
		tage = 0
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET recording_retention_days = $2 WHERE id = $1`, orgID, tage)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
