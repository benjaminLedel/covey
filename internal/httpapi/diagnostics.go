package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/egress"
	"covey/internal/memory"
	"covey/internal/observability"
)

// agentDiagnostics is the complete runtime state of ONE agent as
// self-explanatory JSON — to be pulled from production and analysed elsewhere.
// Unlike the config bundle (export.go) it also contains the recording trace, the
// memory, backlog, cost and egress. Secret VALUES are never included.
type agentDiagnostics struct {
	Kind       string    `json:"kind"`    // "covey.agent-diagnostics"
	Version    int       `json:"version"` // 1
	ExportedAt time.Time `json:"exported_at"`

	Agent           agents.Agent              `json:"agent"`
	EffectiveLevel  string                    `json:"recording_level_effective"`
	Config          *agents.ConfigVersion     `json:"config,omitempty"`
	ConfigHistory   []configVersionMeta       `json:"config_history"`
	Backlog         []backlog.Task            `json:"backlog"`
	Recording       recordingDump             `json:"recording"`
	Memory          memoryDump                `json:"memory"`
	Cost            observability.CostSummary `json:"cost"`
	Egress          egress.AgentEgress        `json:"egress"`
	EgressTemplates []string                  `json:"egress_templates_assigned"`
	SecretKeys      *secretKeyDump            `json:"secret_keys,omitempty"`
	Notes           []string                  `json:"notes,omitempty"`
}

type configVersionMeta struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

type recordingDump struct {
	Events []observability.RecordingEvent `json:"events"`
	Blobs  []diagBlob                     `json:"blobs"`
}

// diagBlob carries a recording artefact (screenshot). Bytes is base64-encoded
// automatically by the JSON encoding.
type diagBlob struct {
	ID        uuid.UUID `json:"id"`
	MIME      string    `json:"mime"`
	Bytes     []byte    `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

type memoryDump struct {
	Pages []memory.Entry    `json:"pages"`
	Log   []memory.LogEntry `json:"log"`
}

type secretKeyDump struct {
	OrgKeys   []string `json:"org_keys"`
	AgentKeys []string `json:"agent_keys"`
}

// Upper bounds: generous, but not unbounded (memory/response size).
const (
	maxDiagEvents  = 200_000
	maxDiagMemory  = 10_000
	diagEventBatch = 1000
)

// handleAgentDiagnostics — GET /api/v1/agents/{id}/diagnostics: an agent's
// complete runtime state as downloadable JSON.
func (s *Server) handleAgentDiagnostics(w http.ResponseWriter, r *http.Request) {
	// agentScoped has already checked the agent and its org membership.
	agent := agentFrom(r)
	id := agent.ID
	p := principalFrom(r)
	ctx := r.Context()

	d := agentDiagnostics{
		Kind:            "covey.agent-diagnostics",
		Version:         1,
		ExportedAt:      time.Now().UTC(),
		Agent:           agent,
		ConfigHistory:   []configVersionMeta{},
		Backlog:         []backlog.Task{},
		EgressTemplates: []string{},
	}

	if lvl, err := s.Obs.EffectiveRecordingLevel(ctx, id); err == nil {
		d.EffectiveLevel = lvl
	}

	// Config: current version in full, plus a slim history.
	if cfg, err := s.Registry.CurrentConfig(ctx, id); err == nil {
		d.Config = &cfg
	}
	if hist, err := s.Registry.ConfigHistory(ctx, id); err == nil {
		for _, m := range hist {
			d.ConfigHistory = append(d.ConfigHistory, configVersionMeta{Version: m.Version, CreatedAt: m.CreatedAt})
		}
	}

	// Backlog including the archive (blocked reasons, errors, results).
	if tasks, err := s.Backlog.ListByAgent(ctx, id, true); err == nil {
		d.Backlog = tasks
	}

	// Recording: all events (paged) + screenshot blobs.
	d.Recording = s.dumpRecording(ctx, id, &d)

	// Memory: wiki pages + log.
	pages, _ := s.Memory.List(ctx, id, maxDiagMemory)
	if pages == nil {
		pages = []memory.Entry{}
	}
	log, _ := s.Memory.Log(ctx, id, maxDiagMemory)
	if log == nil {
		log = []memory.LogEntry{}
	}
	d.Memory = memoryDump{Pages: pages, Log: log}

	// Cost (total).
	if cost, err := s.Obs.CostByAgent(ctx, id); err == nil {
		d.Cost = cost
	}

	// Egress: assignment + names of the assigned templates.
	if s.EgressStore != nil {
		if cfg, err := s.EgressStore.AgentConfig(ctx, id); err == nil {
			d.Egress = cfg
			if tmpls, err := s.EgressStore.ListTemplates(ctx, p.OrgID); err == nil {
				names := map[uuid.UUID]string{}
				for _, t := range tmpls {
					names[t.ID] = t.Name
				}
				for _, tid := range cfg.TemplateIDs {
					if n := names[tid]; n != "" {
						d.EgressTemplates = append(d.EgressTemplates, n)
					}
				}
				sort.Strings(d.EgressTemplates)
			}
		}
	}

	// Secret NAMES (never values), only for authorised roles.
	if canReadSecretKeys(p.Role) {
		d.SecretKeys = s.dumpSecretKeys(ctx, p.OrgID, id)
	} else {
		d.Notes = append(d.Notes, "secret names omitted (missing permission)")
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "diagnostics-"+agent.Slug+".json"))
	writeJSON(w, http.StatusOK, d)
}

// dumpRecording pulls ALL recording events page by page plus the matching
// screenshot blobs. If maxDiagEvents is exceeded the dump is cut off and a note
// is placed in d.Notes.
func (s *Server) dumpRecording(ctx context.Context, agentID uuid.UUID, d *agentDiagnostics) recordingDump {
	dump := recordingDump{Events: []observability.RecordingEvent{}, Blobs: []diagBlob{}}
	var after int64
	for len(dump.Events) < maxDiagEvents {
		batch, err := s.Obs.Events(ctx, agentID, nil, after, diagEventBatch)
		if err != nil || len(batch) == 0 {
			break
		}
		dump.Events = append(dump.Events, batch...)
		after = batch[len(batch)-1].ID
		if len(batch) < diagEventBatch {
			break
		}
	}
	if len(dump.Events) >= maxDiagEvents {
		d.Notes = append(d.Notes, fmt.Sprintf("recording events truncated at %d", maxDiagEvents))
	}

	if blobs, err := s.Obs.BlobsByAgent(ctx, agentID); err == nil {
		for _, b := range blobs {
			dump.Blobs = append(dump.Blobs, diagBlob{ID: b.ID, MIME: b.MIME, Bytes: b.Bytes, CreatedAt: b.CreatedAt})
		}
	}
	return dump
}

// dumpSecretKeys collects the NAMES of the secrets assigned to the agent
// (org-wide + agent-owned) — analogous to the config bundle, without values.
func (s *Server) dumpSecretKeys(ctx context.Context, orgID, agentID uuid.UUID) *secretKeyDump {
	out := &secretKeyDump{OrgKeys: []string{}, AgentKeys: []string{}}
	if previews, err := s.Secrets.Previews(ctx, orgID); err == nil {
		for _, kp := range previews {
			for _, aid := range kp.AgentIDs {
				if aid == agentID.String() {
					out.OrgKeys = append(out.OrgKeys, kp.Key)
				}
			}
		}
	}
	if agentPrev, err := s.Secrets.AgentPreviews(ctx, orgID, agentID); err == nil {
		for _, kp := range agentPrev {
			out.AgentKeys = append(out.AgentKeys, kp.Key)
		}
	}
	sort.Strings(out.OrgKeys)
	sort.Strings(out.AgentKeys)
	return out
}
