package runtimes

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/daemon"
)

// ControlPlaneCredential is the engine access the organisation's seats of one
// engine hold, for the control plane's own model calls — the chat triage, the
// narration, the config copilot, the dream (#483).
//
// Those calls used to look only at the two org secrets the Claude Code engine
// declares, by their exact names. An organisation whose seat carries its token
// under another name had agents that ran and a control plane that reported no
// model: the same credential, read two different ways. This is the second way
// made to agree with the first.
//
// What it reads, and what not:
//
//   - the organisation's runtimes of this engine, their credentials in merit
//     order, and the first one in play — neither paused by hand nor parked by
//     a cooldown. A paused seat is somebody's decision, and a parked one was
//     just rejected or used up; the control plane has no business with either.
//   - the value by the seat's own key AND slot, exactly as Pick delivers it to
//     a sandbox. A seat's credential is always an org-wide value (Value reads
//     agent_id IS NULL): agent-own secrets are not part of any seat and are
//     never read here, so no agent's private access leaks into features that
//     run for the whole organisation.
//
// It records no binding and books nothing: the control plane is not an agent
// and does not take a seat. subscription reports the seat's kind (quota rather
// than metered); what the value itself says about its format is for the
// caller that knows the provider to weigh.
//
// Not found is ErrNotFound — also on a nil store, so that a caller without the
// capacity layer (a test, a tool) needs no guard.
func (s *Store) ControlPlaneCredential(ctx context.Context, orgID uuid.UUID, engine string) (value string, subscription bool, err error) {
	if s == nil || s.pool == nil || s.secrets == nil {
		return "", false, ErrNotFound
	}
	list, err := s.List(ctx, orgID)
	if err != nil {
		return "", false, err
	}
	now := time.Now()
	for _, rt := range list {
		if rt.Engine != engine {
			continue
		}
		for _, c := range rt.Credentials {
			if c.PausedAt != nil || c.parked(now) {
				continue
			}
			v, verr := s.secrets.Value(ctx, orgID, c.SecretKey, c.SecretSlot)
			if verr != nil {
				// A seat pointing at a value that is gone is a fault of that
				// seat, not of the next one.
				continue
			}
			if v = strings.TrimSpace(v); v != "" {
				return v, c.Kind != daemon.CredAPIKey, nil
			}
		}
	}
	return "", false, ErrNotFound
}
