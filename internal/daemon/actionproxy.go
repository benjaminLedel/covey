package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"covey/internal/target"
)

// actionProxy ist der lokale Tool-Layer der Sandbox: die Runtime spricht
// ausschließlich ihn an (http://127.0.0.1:<port>/actions/<system>/<aktion>).
// Jede Aktion wird zentral gegen die Guard-Rails entschieden, mit gebrokerten
// Credentials ausgeführt und aufgezeichnet — Secrets erreichen die Runtime nie.
type actionProxy struct {
	client *Client
	taskID string
	srv    *http.Server
	ln     net.Listener
}

func (c *Client) startActionProxy(ctx context.Context, taskID string) (*actionProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &actionProxy{client: c, taskID: taskID, ln: ln}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /actions/{system}/{action}", p.handle)
	p.srv = &http.Server{Handler: mux, BaseContext: func(net.Listener) context.Context { return ctx }}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *actionProxy) Port() string {
	return strconv.Itoa(p.ln.Addr().(*net.TCPAddr).Port)
}

func (p *actionProxy) Close() error { return p.srv.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (p *actionProxy) handle(w http.ResponseWriter, r *http.Request) {
	system, action := r.PathValue("system"), r.PathValue("action")
	var params json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&params); err != nil {
		params = json.RawMessage(`{}`)
	}

	// "covey" ist kein externes Zielsystem, sondern eine Meta-Aktion an die
	// Control Plane selbst (keine Credentials, keine Guard-Rail-Prüfung).
	if system == "covey" {
		p.handleControlPlane(w, action, params)
		return
	}

	subject := p.actionSubject(r.Context(), system, action, params)

	dec, err := p.client.checkAction(r.Context(), p.taskID, subject, params)
	if err != nil {
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	switch dec.Status {
	case "denied":
		writeJSON(w, map[string]string{"status": "denied", "reason": dec.Reason})
		return
	case "pending":
		writeJSON(w, map[string]string{
			"status":          "pending_approval",
			"approval_id":     dec.ApprovalID,
			"correlation_key": dec.CorrelationKey,
		})
		return
	}

	data, err := p.execute(r.Context(), system, action, params)
	result := map[string]any{"status": "ok", "data": data}
	if err != nil {
		result = map[string]any{"status": "error", "error": err.Error()}
	}
	// Aktion + Ausgang ins Recording (kind=action).
	audit, _ := json.Marshal(map[string]any{"action": subject, "params": params,
		"ok": err == nil})
	_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
	writeJSON(w, result)
}

// handleControlPlane bedient Meta-Aktionen (system="covey"), die die Control
// Plane statt eines Zielsystems betreffen. Derzeit: set_stage — der Agent
// schiebt seine Aufgabe auf dem Board in eine (ggf. neue) Stage.
func (p *actionProxy) handleControlPlane(w http.ResponseWriter, action string, params json.RawMessage) {
	switch action {
	case "set_stage":
		var in struct {
			Stage  string `json:"stage"`
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Stage) == "" {
			writeJSON(w, map[string]string{"status": "error", "error": "stage fehlt"})
			return
		}
		taskID := in.TaskID
		if taskID == "" {
			taskID = p.taskID
		}
		if err := p.client.send(TypeSetStage, SetStage{TaskID: taskID, Stage: in.Stage}); err != nil {
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:set_stage", "stage": in.Stage})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		writeJSON(w, map[string]string{"status": "ok", "stage": in.Stage})
	default:
		writeJSON(w, map[string]string{"status": "error", "error": fmt.Sprintf("unbekannte covey-aktion %q", action)})
	}
}

// actionSubject bildet die Aktion auf das Guard-Rail-Subjekt ab — das
// jeweilige Plugin kennt seine schärfer regelbaren Sonderfälle (z. B.
// zammad:reply_external). Unbekannte Systeme fallen auf system:aktion zurück.
func (p *actionProxy) actionSubject(ctx context.Context, system, action string, params json.RawMessage) string {
	if sys, ok := p.resolveSystem(ctx, system); ok {
		return sys.ActionSubject(action, params)
	}
	return system + ":" + action
}

// resolveSystem findet das Zielsystem: zuerst die kompilierte Plugin-Registry,
// dann die von der Control Plane gebrokerten Manifest-Plugins (custom).
func (p *actionProxy) resolveSystem(ctx context.Context, system string) (target.System, bool) {
	if sys, ok := target.Get(system); ok {
		return sys, true
	}
	return p.client.manifestSystem(ctx, system)
}

// execute führt die Aktion mit gebrokerten Credentials aus. Welche Systeme es
// gibt, entscheidet die Plugin-Registry (bzw. das Manifest) — nicht dieser Code.
func (p *actionProxy) execute(ctx context.Context, system, action string, params json.RawMessage) (any, error) {
	sys, ok := p.resolveSystem(ctx, system)
	if !ok {
		return nil, fmt.Errorf("unbekanntes zielsystem %q", system)
	}
	cred, err := p.client.credential(ctx, system, p.taskID)
	if err != nil {
		return nil, err
	}
	return sys.Execute(ctx, action, params, target.Credential{BaseURL: cred.BaseURL, Token: cred.Token})
}
