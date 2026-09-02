package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/notify"
	"covey/internal/observability"
	"covey/internal/secrets"
)

// The life of a target-system credential, from the control plane's side.
//
// A token expires, and until #176 the first sign was a 401 in a recording,
// filed under the action that hit it — which reads as a permission problem
// three weeks later. Two signals now reach the stored secret instead:
//
//   - the hard one: a run's action was refused with a 401. The daemon says so
//     in the action event, and noteTargetRejection marks the secret the run
//     was given. That is the counterpart of noteCredentialRejection, which
//     does the same for the LLM seat.
//   - the soft one: a daily probe of every stored token, through the plugin's
//     Inspect (lifetime and all) or Probe (works or not). The probe is what
//     the setup wizard runs once; here it runs without anybody pressing.
//
// What the probe learns is acted on: a value that can mint its successor and
// is about to run out is rotated, and a value that cannot — or is already
// refused — is reported to a person, once, until its state changes. The lint
// (internal/agents) reads the same columns, so the finding stands where the
// person looks.

const (
	// credentialCheckEvery: how often the tokens are probed. A day — a token
	// lives months, and a probe is a request against somebody's system.
	credentialCheckEvery = 24 * time.Hour
	// credentialCheckDelay: the first probe after a start waits, so that a
	// restarting instance does not open with a burst against every target.
	credentialCheckDelay = 2 * time.Minute
	// credentialWarnAhead: from here on a person hears about an expiry.
	credentialWarnAhead = 14 * 24 * time.Hour
	// credentialRotateAhead: from here on a rotatable value is rotated. Wider
	// than the warning on purpose — a rotation that works moves the date out
	// before anybody has to hear about it, and one that fails leaves two
	// weeks for a person.
	credentialRotateAhead = 30 * 24 * time.Hour
	// credentialProbeTimeout: one probe, one bound.
	credentialProbeTimeout = 15 * time.Second
	// tokenSuffix is how a target system's credential is keyed (spec/04).
	tokenSuffix = "_token"
)

// credentialLoop probes the stored tokens once a day.
func (o *Orchestrator) credentialLoop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(credentialCheckDelay):
	}
	t := time.NewTicker(credentialCheckEvery)
	defer t.Stop()
	o.CheckCredentials(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.CheckCredentials(ctx)
		}
	}
}

// CheckCredentials probes every stored target-system token, rotates what is
// about to run out and can be, and tells somebody about what cannot. Exported
// so that a test or a command can run it without waiting a day.
func (o *Orchestrator) CheckCredentials(ctx context.Context) {
	if o.Secrets == nil || o.Targets == nil {
		return
	}
	stored, err := o.Secrets.List(ctx, uuid.Nil, tokenSuffix)
	if err != nil {
		o.Log.Warn("credential check: tokens not listed", "err", err)
		return
	}
	for _, st := range stored {
		if ctx.Err() != nil {
			return
		}
		o.checkCredential(ctx, st)
	}
}

// checkCredential is one token's turn: probe, rotate if due, warn if due.
func (o *Orchestrator) checkCredential(ctx context.Context, st secrets.Stored) {
	system := strings.TrimSuffix(st.Key, tokenSuffix)
	if system == "" {
		return
	}
	// Definition, not System: a token stored for a plugin nobody has switched
	// on yet is still a token that expires. Keys that are not a target
	// system's (claude_code_oauth_token) find no plugin and are left alone.
	sys, err := o.Targets.Definition(ctx, st.OrgID, system)
	if err != nil {
		return
	}
	inspector, inspects := target.Inspects(sys)
	prober, probes := target.Probes(sys)
	if !inspects && !probes {
		return
	}
	cred, ok := o.storedCredential(ctx, st, system)
	if !ok {
		return
	}

	now := time.Now()
	info, err := o.probeStored(ctx, inspector, prober, cred)
	rec := secrets.Probe{At: now, Identity: info.Identity, ExpiresAt: info.ExpiresAt,
		CredentialID: info.ID, Rotatable: info.Rotatable}
	if err != nil {
		rec.Err, rec.Rejected = err.Error(), daemon.CredentialRejected(err)
	}
	if err := o.Secrets.RecordProbe(ctx, st.Ref, rec); err != nil {
		o.Log.Warn("credential check: probe not recorded", "key", st.Key, "err", err)
		return
	}
	if rec.Rejected {
		o.recordCredential(ctx, st, map[string]any{"system": system, "granted": false, "reason": "rejected", "error": rec.Err})
	}

	// What is known after the probe: the plugin's word where it has one, the
	// stored date otherwise (entered by a person, or reported last time).
	expiresAt := st.ExpiresAt
	if info.ExpiresAt != nil {
		expiresAt = info.ExpiresAt
	}
	credentialID := st.CredentialID
	if info.ID != "" {
		credentialID = info.ID
	}
	// A probe that fails for another reason (no route, a 502) does not unsay
	// a rejection that stands; a probe that works does — and with it the
	// warning that was about it.
	rejected, reason := rec.Rejected, rec.Err
	if !rejected && err != nil && st.RejectedAt != nil {
		rejected, reason = true, st.RejectedReason
	}
	warned := st.WarnedAt != nil && !(err == nil && st.RejectedAt != nil)

	if err == nil && info.Rotatable && expiresAt != nil && expiresAt.Sub(now) < credentialRotateAhead {
		if o.rotateStored(ctx, st, system, sys, cred, credentialID) {
			return // a successor with a fresh date — nothing to warn about
		}
	}

	warn := ""
	switch {
	case rejected:
		warn = fmt.Sprintf("%s refused the credential %s: %s", systemLabel(system), st.Key, firstLine(reason))
	case expiresAt != nil && now.After(*expiresAt):
		warn = fmt.Sprintf("%s has expired (%s) — %s will refuse it", st.Key, expiresAt.Format("2006-01-02"), systemLabel(system))
	case expiresAt != nil && expiresAt.Sub(now) < credentialWarnAhead:
		days := int(expiresAt.Sub(now).Hours() / 24)
		warn = fmt.Sprintf("%s expires on %s (in %d days)", st.Key, expiresAt.Format("2006-01-02"), days)
	}
	if warn == "" || warned {
		return
	}
	o.warnCredential(ctx, st, warn)
}

// storedCredential assembles what the run would get for this row: the token
// itself, and the endpoint and trust anchor that go with it — from the agent's
// view for an agent's own token, from the organisation's for an org-wide one.
func (o *Orchestrator) storedCredential(ctx context.Context, st secrets.Stored, system string) (target.Credential, bool) {
	token, err := o.Secrets.Open(ctx, st.Ref)
	if err != nil {
		o.Log.Warn("credential check: token not readable", "key", st.Key, "err", err)
		return target.Credential{}, false
	}
	sibling := func(key string) string {
		if st.AgentID != nil {
			v, _ := o.Secrets.Resolve(ctx, st.OrgID, *st.AgentID, key)
			return v
		}
		v, _ := o.Secrets.Get(ctx, st.OrgID, key)
		return v
	}
	cred := target.Credential{Token: token, BaseURL: sibling(system + "_url"), CA: sibling(system + "_ca")}
	if cred.BaseURL == "" {
		if d, ok := target.Describe(system); !ok || !d.BaseURLOptional {
			// Half a setup. The wizard says so where it is being set up; a
			// probe against nowhere would only write "no URL" beside the
			// token every day.
			return target.Credential{}, false
		}
	}
	return cred, true
}

// probeStored asks the plugin the richest question it answers.
func (o *Orchestrator) probeStored(ctx context.Context, inspector target.CredentialInspector, prober target.Prober, cred target.Credential) (target.CredentialInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, credentialProbeTimeout)
	defer cancel()
	if inspector != nil {
		return inspector.Inspect(ctx, cred)
	}
	identity, err := prober.Probe(ctx, cred)
	return target.CredentialInfo{Identity: identity}, err
}

// rotateStored mints the successor and puts it in the old one's place. The
// order is the SDK's contract: mint, verify, store, and only then revoke —
// a rotation that revoked first and then failed would leave the agent with
// nothing. Reports whether the row now carries a verified successor.
func (o *Orchestrator) rotateStored(ctx context.Context, st secrets.Stored, system string, sys target.System, cred target.Credential, oldID string) bool {
	rotator, ok := target.Rotates(sys)
	if !ok {
		return false
	}
	fail := func(step string, err error) bool {
		o.Log.Warn("credential rotation failed", "key", st.Key, "step", step, "err", err)
		o.recordCredential(ctx, st, map[string]any{"system": system, "rotated": false, "step": step, "error": err.Error()})
		return false
	}
	rctx, cancel := context.WithTimeout(ctx, 2*credentialProbeTimeout)
	defer cancel()
	next, info, err := rotator.Rotate(rctx, cred)
	if err != nil {
		return fail("mint", err)
	}
	inspector, _ := target.Inspects(sys)
	prober, _ := target.Probes(sys)
	seen, err := o.probeStored(ctx, inspector, prober, next)
	if err != nil {
		return fail("verify", err)
	}
	lt := secrets.Lifetime{ExpiresAt: info.ExpiresAt, CredentialID: info.ID, Rotatable: true}
	if err := o.Secrets.Replace(ctx, st.Ref, next.Token, lt); err != nil {
		return fail("store", err)
	}
	if err := o.Secrets.RecordProbe(ctx, st.Ref, secrets.Probe{At: time.Now(), Identity: seen.Identity,
		ExpiresAt: info.ExpiresAt, CredentialID: info.ID, Rotatable: true}); err != nil {
		o.Log.Warn("credential rotation: probe not recorded", "key", st.Key, "err", err)
	}
	if err := rotator.Revoke(rctx, next, oldID); err != nil {
		// The successor is in place; the predecessor runs out on its own.
		o.Log.Warn("credential rotation: predecessor not revoked", "key", st.Key, "id", oldID, "err", err)
	}
	payload := map[string]any{"system": system, "rotated": true, "credential_id": info.ID}
	if info.ExpiresAt != nil {
		payload["expires_at"] = info.ExpiresAt
	}
	o.recordCredential(ctx, st, payload)
	o.Log.Info("credential rotated", "key", st.Key, "system", system, "expires_at", info.ExpiresAt)
	return true
}

// noteTargetRejection is the hard signal: an action was refused because of
// the credential. The secret the run was given is marked — the agent's own
// before the assigned org-wide one, as Resolve chose it — and a person hears
// about it once.
func (o *Orchestrator) noteTargetRejection(ctx context.Context, agent agents.Agent, payload json.RawMessage) {
	var ev struct {
		Action   string `json:"action"`
		Rejected bool   `json:"credential_rejected"`
		Error    string `json:"error"`
	}
	if json.Unmarshal(payload, &ev) != nil || !ev.Rejected {
		return
	}
	system, _, _ := strings.Cut(ev.Action, ":")
	if system == "" || o.Secrets == nil {
		return
	}
	st, news, err := o.Secrets.MarkRejected(ctx, agent.OrgID, agent.ID, system+tokenSuffix, firstLine(ev.Error))
	if err != nil {
		// A plugin without credentials, or one whose credential is optional
		// and absent: nothing to mark.
		return
	}
	if !news {
		return
	}
	o.Log.Warn("target system refused the credential", "agent", agent.Slug, "system", system, "key", st.Key)
	o.recordCredential(ctx, st, map[string]any{"system": system, "granted": false, "reason": "rejected",
		"error": firstLine(ev.Error), "agent": agent.ID})
	if st.WarnedAt == nil {
		o.warnCredential(ctx, st, fmt.Sprintf("%s refused the credential %s: %s", systemLabel(system), st.Key, firstLine(ev.Error)))
	}
}

// warnCredential tells a person, and notes that it did — the note goes when
// the state does (a new value, a probe that works, a new date).
func (o *Orchestrator) warnCredential(ctx context.Context, st secrets.Stored, title string) {
	ev := notify.Event{OrgID: st.OrgID, Class: notify.ClassOps, Kind: notify.KindCredential, Title: title, Link: "/secrets"}
	if st.AgentID != nil {
		ev.AgentID = *st.AgentID
		ev.Link = "/agents/" + st.AgentID.String()
		ev.Title = title + " — own secret of " + o.agentName(ctx, *st.AgentID)
	}
	o.emit(ctx, ev)
	if err := o.Secrets.MarkWarned(ctx, st.Ref); err != nil {
		o.Log.Warn("credential warning not noted", "key", st.Key, "err", err)
	}
}

// recordCredential writes a credential event into the recording of every
// agent the value reaches: the owner of an agent's own value, the assignees
// of an org-wide one. The recording is an agent's story, and a token that was
// rotated or refused is a chapter of the story of each agent that runs on it.
func (o *Orchestrator) recordCredential(ctx context.Context, st secrets.Stored, payload map[string]any) {
	if o.Obs == nil {
		return
	}
	payload["key"] = st.Key
	agentIDs := []uuid.UUID{}
	if st.AgentID != nil {
		agentIDs = append(agentIDs, *st.AgentID)
	} else if o.Pool != nil {
		rows, err := o.Pool.Query(ctx, `SELECT agent_id FROM secret_assignments WHERE org_id=$1 AND key=$2`, st.OrgID, st.Key)
		if err != nil {
			o.Log.Warn("credential event: assignees not read", "key", st.Key, "err", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var id uuid.UUID
			if rows.Scan(&id) == nil {
				agentIDs = append(agentIDs, id)
			}
		}
	}
	for _, id := range agentIDs {
		_ = o.Obs.Record(ctx, st.OrgID, id, nil, observability.KindCredential, payload)
	}
}

// systemLabel is the plugin's display name, or the name where there is none.
func systemLabel(system string) string {
	if d, ok := target.Describe(system); ok && d.Label != "" {
		return d.Label
	}
	return system
}

// firstLine keeps an error to what fits beside a value.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}
