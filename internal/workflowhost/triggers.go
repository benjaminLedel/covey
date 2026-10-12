package workflowhost

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/workflow"
)

// Triggers (#566): a workflow starts from the sources agents already wake
// from. The schedule and its gate fire from the host's clock; webhooks and the
// token trigger arrive through the API and come here.

// ErrNoTrigger: the workflow does not take what was delivered.
var ErrNoTrigger = errors.New("the workflow does not take this trigger")

// fireSchedules starts runs for every workflow whose schedule is due — the
// heartbeat's rule: due by `every` since the last firing, or once a day after
// `daily`; with a gate, only when it sees work, and not again for the same
// work signature.
func (h *Host) fireSchedules(ctx context.Context) {
	h.mu.Lock()
	var list []*hosted
	for _, hw := range h.engines {
		list = append(list, hw)
	}
	h.mu.Unlock()
	now := h.now()
	for _, hw := range list {
		t := hw.eng.Trigger()
		if t == nil || hw.eng.Mode() == workflow.ModeOff || (t.Every == 0 && t.Daily == "") {
			continue
		}
		state, err := h.Store.Trigger(ctx, hw.stored.ID)
		if err != nil {
			h.log().Warn("workflow trigger state not readable", "workflow", hw.stored.Slug, "err", err)
			continue
		}
		if !due(t, state.LastFired, now) {
			continue
		}
		sig := ""
		if t.OnlyIf != "" && h.Platform != nil {
			agent, err := h.colleague(ctx, hw.stored.OrgID, hw.stored.ID, t.As)
			if err != nil {
				h.log().Warn("workflow gate: no colleague to check as", "workflow", hw.stored.Slug, "err", err)
				_ = h.Store.Fired(ctx, hw.stored.ID, now, state.LastSig)
				continue
			}
			has, s := h.Platform.HasWork(ctx, hw.stored.OrgID, agent.ID, t.OnlyIf)
			if !has {
				_ = h.Store.Fired(ctx, hw.stored.ID, now, "")
				continue
			}
			// The same work as last time is not new work.
			if s != "" && s == state.LastSig {
				_ = h.Store.Fired(ctx, hw.stored.ID, now, s)
				continue
			}
			sig = s
		}
		if err := h.Store.Fired(ctx, hw.stored.ID, now, sig); err != nil {
			h.log().Warn("workflow trigger state not written", "workflow", hw.stored.Slug, "err", err)
			continue
		}
		title := t.Title
		if title == "" {
			title = hw.stored.Def.Name
		}
		caseRef := "schedule:" + now.Format("2006-01-02T15:04")
		if _, err := hw.eng.Start(caseRef, map[string]string{"title": title + " · " + now.Format("2006-01-02 15:04"), "no": caseRef}); err != nil {
			h.log().Warn("workflow schedule: run not started", "workflow", hw.stored.Slug, "err", err)
		}
	}
}

// due: the heartbeat's two schedules.
func due(t *workflow.Step, last *time.Time, now time.Time) bool {
	if t.Every > 0 {
		return last == nil || !now.Before(last.Add(t.Every))
	}
	at, err := time.ParseInLocation("15:04", t.Daily, now.Location())
	if err != nil {
		return false
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	return !now.Before(today) && (last == nil || last.Before(today))
}

// Delivery is what a webhook or the token trigger brings.
type Delivery struct {
	// Source names the trigger: "webhook:<system>" or "token".
	Source string
	// Case is the case's key: the event's correlation key, the trigger's
	// case or dedup key.
	Case  string
	Title string
	Body  string
	// DedupKey keeps one delivery from being taken twice.
	DedupKey string
	// Start: a delivery for a case without an open run starts one. A webhook
	// event that only correlates does not.
	Start bool
}

// Deliver takes a webhook event or a token trigger for a workflow. A case
// with an open run gets the delivery as an event its wait step may be
// waiting for; a new case starts a run.
func (h *Host) Deliver(ctx context.Context, workflowID uuid.UUID, d Delivery) (string, error) {
	h.mu.Lock()
	hw := h.engines[workflowID]
	h.mu.Unlock()
	if hw == nil {
		return "", workflow.ErrNotFound
	}
	t := hw.eng.Trigger()
	switch {
	case t == nil:
		return "", ErrNoTrigger
	case strings.HasPrefix(d.Source, "webhook:") && t.Webhook != strings.TrimPrefix(d.Source, "webhook:"):
		return "", fmt.Errorf("%w: it takes webhooks from %q", ErrNoTrigger, t.Webhook)
	case d.Source == "token" && !t.Token:
		return "", ErrNoTrigger
	}
	if hw.eng.Mode() == workflow.ModeOff {
		return "off", nil
	}
	if d.DedupKey != "" {
		fresh, err := h.Store.Fresh(ctx, "workflow:"+workflowID.String()+":"+d.DedupKey, d.Source)
		if err != nil {
			return "", err
		}
		if !fresh {
			return "duplicate", nil
		}
	}
	caseRef := d.Case
	if caseRef == "" {
		caseRef = d.DedupKey
	}
	if caseRef == "" {
		caseRef = uuid.NewString()
	}
	if run, ok := hw.eng.OpenRun(caseRef); ok {
		// Delivered to a step that waits for it, or noted as nothing to do.
		if err := hw.eng.Event(run.ID, d.Source); err != nil && !errors.Is(err, workflow.ErrWrongState) {
			return "", err
		}
		return "delivered", nil
	}
	if !d.Start {
		return "ignored", nil
	}
	vars := map[string]string{"no": caseRef}
	if d.Title != "" {
		vars["title"] = d.Title
	}
	if d.Body != "" {
		vars["body"] = d.Body
	}
	if _, err := hw.eng.Start(caseRef, vars); err != nil {
		return "", err
	}
	return "started", nil
}

// ByID finds a loaded workflow by its id, for the webhook route.
func (h *Host) ByID(id uuid.UUID) (workflow.Stored, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	hw := h.engines[id]
	if hw == nil {
		return workflow.Stored{}, false
	}
	return hw.stored, true
}
