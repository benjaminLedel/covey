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

// agentDiagnostics ist der vollständige Laufzeit-Zustand EINES Agenten als
// selbst-erklärendes JSON — zum Ziehen aus Produktiv und Analysieren anderswo.
// Anders als das Config-Bundle (export.go) enthält es auch den Recording-Trace,
// das Gedächtnis, Backlog, Kosten und Egress. Secret-WERTE sind nie enthalten.
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

// diagBlob trägt ein Recording-Artefakt (Screenshot). Bytes wird von der
// JSON-Kodierung automatisch base64-kodiert.
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

// Obergrenzen: großzügig, aber nicht unbegrenzt (Speicher/Antwortgröße).
const (
	maxDiagEvents  = 200_000
	maxDiagMemory  = 10_000
	diagEventBatch = 1000
)

// handleAgentDiagnostics — GET /api/v1/agents/{id}/diagnostics: der komplette
// Laufzeit-Zustand eines Agenten als herunterladbares JSON.
func (s *Server) handleAgentDiagnostics(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	ctx := r.Context()

	agent, err := s.Registry.Get(ctx, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if agent.OrgID != p.OrgID {
		writeErr(w, http.StatusNotFound, "agent nicht gefunden")
		return
	}

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

	// Config: aktuelle Version voll, plus schlanke Historie.
	if cfg, err := s.Registry.CurrentConfig(ctx, id); err == nil {
		d.Config = &cfg
	}
	if rows, err := s.Pool.Query(ctx, `SELECT version, created_at FROM agent_config_versions WHERE agent_id=$1 ORDER BY version`, id); err == nil {
		defer rows.Close()
		for rows.Next() {
			var m configVersionMeta
			if rows.Scan(&m.Version, &m.CreatedAt) == nil {
				d.ConfigHistory = append(d.ConfigHistory, m)
			}
		}
	}

	// Backlog inkl. Archiv (blocked-Gründe, Fehler, Ergebnisse).
	if tasks, err := s.Backlog.ListByAgent(ctx, id, true); err == nil {
		d.Backlog = tasks
	}

	// Recording: alle Events (seitenweise) + Screenshot-Blobs.
	d.Recording = s.dumpRecording(ctx, id, &d)

	// Gedächtnis: Wiki-Seiten + Protokoll.
	pages, _ := s.Memory.List(ctx, id, maxDiagMemory)
	if pages == nil {
		pages = []memory.Entry{}
	}
	log, _ := s.Memory.Log(ctx, id, maxDiagMemory)
	if log == nil {
		log = []memory.LogEntry{}
	}
	d.Memory = memoryDump{Pages: pages, Log: log}

	// Kosten (Summe).
	if cost, err := s.Obs.CostByAgent(ctx, id); err == nil {
		d.Cost = cost
	}

	// Egress: Zuweisung + Namen der zugewiesenen Templates.
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

	// Secret-NAMEN (nie Werte), nur für berechtigte Rollen.
	if canReadSecretKeys(p.Role) {
		d.SecretKeys = s.dumpSecretKeys(ctx, p.OrgID, id)
	} else {
		d.Notes = append(d.Notes, "Secret-Namen ausgelassen (fehlende Berechtigung)")
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", "diagnostics-"+agent.Slug+".json"))
	writeJSON(w, http.StatusOK, d)
}

// dumpRecording zieht ALLE Recording-Events seitenweise und die zugehörigen
// Screenshot-Blobs. Bei Überschreiten von maxDiagEvents wird abgeschnitten und
// ein Hinweis in d.Notes gesetzt.
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
		d.Notes = append(d.Notes, fmt.Sprintf("Recording-Events bei %d abgeschnitten", maxDiagEvents))
	}

	rows, err := s.Pool.Query(ctx, `SELECT id, mime, bytes, created_at FROM recording_blobs WHERE agent_id=$1 ORDER BY created_at`, agentID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var b diagBlob
			if rows.Scan(&b.ID, &b.MIME, &b.Bytes, &b.CreatedAt) == nil {
				dump.Blobs = append(dump.Blobs, b)
			}
		}
	}
	return dump
}

// dumpSecretKeys sammelt die NAMEN der dem Agenten zugewiesenen Secrets
// (org-weite + agent-eigene) — analog zum Config-Bundle, ohne Werte.
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
