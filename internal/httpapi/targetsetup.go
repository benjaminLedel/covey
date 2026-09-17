package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/secrets"
	"covey/internal/target/manifestplug"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Setup of a target system — the state that the assistant drives.
//
// Until now setup stood as running text in SetupDoc, and each step in it led
// somewhere else: secrets to one side, ACCESS.md into the agent's config
// editor, the webhook URL one had to compose oneself from the public address
// and an agent slug. Whether it all fitted at the end showed itself on the
// first run.
//
// This endpoint answers the same questions as state instead of prose: which
// credentials does the plugin need and are they present? Which scopes does it
// know? Does it accept webhooks, and what does the address read then? Which
// agent already has it in its ACCESS.md? The prose part stays — but only for
// what is to be done in the FOREIGN system, since this surface cannot do that
// for someone.

type setupCredential struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // "url" | "token"
	// Stored: a value exists org-wide. Not the value itself — that is
	// write-only and stays that way for an assistant too.
	Stored bool `json:"stored"`
	// Optional: the plugin also works without this value.
	Optional bool `json:"optional"`
}

type setupWebhook struct {
	Supported bool `json:"supported"`
	// URL with the real public address; <agent-slug> stays as a placeholder
	// until the assistant knows the agent.
	URL string `json:"url,omitempty"`
	// SecretEnv is the process variable holding the HMAC secret, SecretSet
	// says whether it is set. Without it the endpoint does accept, but
	// unchecked — and one should see that before using it in production.
	SecretEnv string `json:"secret_env,omitempty"`
	SecretSet bool   `json:"secret_set"`
}

type setupAgent struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	// Access: the agent has the system in its ACCESS.md.
	Access bool     `json:"access"`
	Scopes []string `json:"scopes,omitempty"`
}

type setupState struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Enabled     bool              `json:"enabled"`
	Credentials []setupCredential `json:"credentials"`
	Scopes      []string          `json:"scopes,omitempty"`
	Webhook     setupWebhook      `json:"webhook"`
	// Probe: the plugin can check a connection. Where it cannot, the assistant
	// skips the step visibly, instead of setting a checkmark for which it has
	// no evidence.
	Probe    bool         `json:"probe"`
	Agents   []setupAgent `json:"agents"`
	SetupDoc string       `json:"setup_doc,omitempty"`
}

func (s *Server) handleTargetSetup(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")

	list, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	var found bool
	var state setupState
	var kind string
	var definition json.RawMessage
	for _, pl := range list {
		if pl.Name != name {
			continue
		}
		found = true
		kind, definition = pl.Kind, pl.Manifest
		state = setupState{
			Name:     pl.Name,
			Label:    pl.Label,
			Enabled:  pl.Enabled,
			SetupDoc: strings.ReplaceAll(pl.SetupDoc, "{public_url}", s.origin(r)),
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "unknown target system")
		return
	}
	// The flags (which credentials, which scopes) stand in the descriptor of
	// the compiled plugin. Manifest and MCP plugins have none — there the
	// fields stay empty, and the assistant shows correspondingly less.
	plugin, _ := target.Describe(name)
	state.Scopes = plugin.Scopes
	// A manifest plugin has no descriptor, but its scope vocabulary stands in
	// the file — otherwise the assistant would offer no scopes at all for
	// catalogue plugins and every word in ACCESS.md would be guesswork.
	if len(state.Scopes) == 0 && kind == "custom" {
		if m, err := manifestplug.Parse(definition); err == nil {
			state.Scopes = m.Scopes
		}
	}

	// Which credentials the plugin needs stands in its flags — the naming
	// convention <system>_url/<system>_token is the same one the broker
	// resolves at run time.
	if !plugin.NoCredentials {
		keys, err := s.Secrets.Keys(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		has := map[string]bool{}
		for _, k := range keys {
			has[k] = true
		}
		if !plugin.BaseURLOptional {
			state.Credentials = append(state.Credentials, setupCredential{
				Key: name + "_url", Kind: "url", Stored: has[name+"_url"],
			})
		} else {
			state.Credentials = append(state.Credentials, setupCredential{
				Key: name + "_url", Kind: "url", Stored: has[name+"_url"], Optional: true,
			})
		}
		state.Credentials = append(state.Credentials, setupCredential{
			Key: name + "_token", Kind: "token", Stored: has[name+"_token"],
			Optional: plugin.CredentialsOptional,
		})
	}

	// Webhook: whether a plugin accepts any does not stand in a list, but in
	// what it implements (target.Webhooker). Hence it is asked here, not looked
	// up.
	// Definition instead of target.Get: a manifest plugin is not in the
	// compiled registry, yet it may just as well accept a webhook and test its
	// connection — it only says so in its file instead of its method set.
	// target.Probes asks both together.
	if sys, err := s.Targets.Definition(r.Context(), p.OrgID, name); err == nil {
		if _, isHook := sys.(target.Webhooker); isHook {
			env := "COVEY_" + strings.ToUpper(name) + "_WEBHOOK_SECRET"
			state.Webhook = setupWebhook{
				Supported: true,
				URL:       s.origin(r) + "/api/webhooks/" + name + "/<agent-slug>",
				SecretEnv: env,
				SecretSet: s.WebhookSecrets[name] != "",
			}
		}
		_, state.Probe = target.Probes(sys)
	}

	// Who already has the system? The answer stands in the ACCESS.md of the
	// agents, that is where it holds too.
	agentList, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for _, a := range agentList {
		entry := setupAgent{ID: a.ID, Slug: a.Slug, DisplayName: a.DisplayName}
		if cfg, err := s.Registry.CurrentConfig(r.Context(), a.ID); err == nil {
			for _, acc := range agents.ParseAccess(cfg.Files["ACCESS.md"]) {
				if acc.System == name {
					entry.Access = true
					entry.Scopes = acc.Scopes
					break
				}
			}
		}
		state.Agents = append(state.Agents, entry)
	}
	// Empty lists are empty lists and not null. The field is called
	// `credentials` without omitempty, the surface reads it as an array — a
	// nil slice does become null in JSON, and `null.length` ends the setup
	// with a TypeError before it displays anything. It hit exactly the systems
	// that need no credentials (browser, dev): there no append ever hangs on
	// the case distinction above.
	if state.Credentials == nil {
		state.Credentials = []setupCredential{}
	}
	if state.Agents == nil {
		state.Agents = []setupAgent{}
	}

	writeJSON(w, http.StatusOK, state)
}

// handleTargetProbe asks the one question that tells "saved" from
// "works": does the system answer to the stored credentials, and as
// whom.
//
// The call is read-only and runs in the control plane — the token does not
// leave it toward the sandbox on the way.
type probeResult struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"`
	Error    string `json:"error,omitempty"`
	// What the plugin knows about the credential's life, where it does
	// (target.CredentialInspector): the date it runs out, and whether covey
	// can renew it itself.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Rotatable bool       `json:"rotatable,omitempty"`
}

func (s *Server) handleTargetProbe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")

	sys, err := s.Targets.Definition(r.Context(), p.OrgID, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown target system")
		return
	}
	agentID, ok := s.probeAgent(w, r, p.OrgID)
	if !ok {
		return
	}
	inspector, inspects := target.Inspects(sys)
	prober, probes := target.Probes(sys)
	if !inspects && !probes {
		writeErr(w, http.StatusBadRequest, "this target system cannot test its connection")
		return
	}

	d, _ := target.Describe(name)
	var cred target.Credential
	// ref addresses the value the test used, so the outcome is written beside
	// the one a run would get — an agent's own where the test ran as an agent.
	ref := secrets.Ref{OrgID: p.OrgID, Key: name + "_token"}
	if !d.NoCredentials {
		var token string
		if agentID != nil {
			// Lookup names the value Resolve would hand this agent; Open reads
			// it. "For this agent" in the error is the whole point: an
			// organisation secret that reaches nobody is not stored for it.
			st, err := s.Secrets.Lookup(r.Context(), p.OrgID, *agentID, ref.Key)
			if err == nil {
				ref = st.Ref
				token, err = s.Secrets.Open(r.Context(), st.Ref)
			}
			if err != nil {
				writeJSON(w, http.StatusOK, probeResult{Error: "no " + name + "_token stored for this agent"})
				return
			}
		} else {
			token, err = s.orgSecret(r.Context(), p.OrgID, ref.Key)
			if err != nil {
				writeJSON(w, http.StatusOK, probeResult{Error: "no " + name + "_token stored"})
				return
			}
		}
		base, err := s.probeSecret(r.Context(), p.OrgID, agentID, name+"_url")
		if err != nil && !d.BaseURLOptional {
			writeJSON(w, http.StatusOK, probeResult{Error: "no " + name + "_url stored"})
			return
		}
		// The connection test uses the same trust anchor the runs will: a
		// probe that trusts more than the action does would report a health
		// the agent never gets to see.
		ca, _ := s.probeSecret(r.Context(), p.OrgID, agentID, name+"_ca")
		cred = target.Credential{BaseURL: base, Token: token, CA: ca}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var info target.CredentialInfo
	if inspects {
		info, err = inspector.Inspect(ctx, cred)
	} else {
		info.Identity, err = prober.Probe(ctx, cred)
	}
	// What the test saw is kept beside the token — the same columns the
	// daily check writes, so the wizard's button and the loop tell one
	// story. A system without credentials has no row to write to.
	if !d.NoCredentials {
		rec := secrets.Probe{At: time.Now(), Identity: info.Identity, ExpiresAt: info.ExpiresAt,
			CredentialID: info.ID, Rotatable: info.Rotatable}
		if err != nil {
			rec.Err, rec.Rejected = err.Error(), daemon.CredentialRejected(err)
		}
		_ = s.Secrets.RecordProbe(r.Context(), ref, rec)
	}
	if err != nil {
		// The target system's error message stands here deliberately as it
		// arrived: "HTTP 401" is the most useful answer there is for whoever
		// just put a token in.
		writeJSON(w, http.StatusOK, probeResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, probeResult{OK: true, Identity: info.Identity, ExpiresAt: info.ExpiresAt, Rotatable: info.Rotatable})
}

// orgSecret reads an org-wide value (slot 0) — the one the broker would also
// take at run time.
func (s *Server) orgSecret(ctx context.Context, orgID uuid.UUID, key string) (string, error) {
	return s.Secrets.Value(ctx, orgID, key, 0)
}

// probeAgent reads the agent the test is to run as: optional, named as
// "agent_id" in the body. It reports the agent and whether the request may go
// on — where it may not, the answer has already been written.
//
// The test could not name an agent at all until now, and read organisation
// secrets only. For an agent-scoped credential — what the platform recommends
// wherever a credential belongs to one employee, its own bot account, its own
// mail — it therefore reported a fault that was not there (#189). An empty
// body stays the organisation-wide test it always was.
func (s *Server) probeAgent(w http.ResponseWriter, r *http.Request, orgID uuid.UUID) (*uuid.UUID, bool) {
	var body struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // no body, no agent
	if body.AgentID == "" {
		return nil, true
	}
	id, err := uuid.Parse(body.AgentID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "agent_id is not an id")
		return nil, false
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil || a.OrgID != orgID {
		writeErr(w, http.StatusNotFound, "unknown agent")
		return nil, false
	}
	return &id, true
}

// probeSecret reads a value beside the token — as the named agent where one is
// named, exactly as the dispatcher does (the agent's own before the
// organisation's, and an organisation secret only where it is assigned).
func (s *Server) probeSecret(ctx context.Context, orgID uuid.UUID, agentID *uuid.UUID, key string) (string, error) {
	if agentID != nil {
		return s.Secrets.Resolve(ctx, orgID, *agentID, key)
	}
	return s.orgSecret(ctx, orgID, key)
}
