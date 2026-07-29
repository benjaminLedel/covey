package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	"covey/internal/reqlog"
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
		p.handleControlPlane(r.Context(), w, action, params)
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

	// Artefakt-Senke: ein Plugin (z. B. browser screenshot) kann ein Bild fürs
	// Recording durchreichen, ohne es ins Runtime-Ergebnis zu legen.
	var artifact *target.Artifact
	ctx := target.WithArtifactSink(r.Context(), func(a target.Artifact) { artifact = &a })
	// Request-Senke: die HTTP-Requests, die das Plugin auf dem Weg stellt,
	// gehen als eigene Events an die Control Plane (Request-Log). Die Senke
	// hängt am Context, damit sie nur für diese Aktion gilt.
	ctx = reqlog.WithSink(ctx, p.httpSink(system))

	data, err := p.execute(ctx, system, action, params)
	result := map[string]any{"status": "ok", "data": data}
	if err != nil {
		result = map[string]any{"status": "error", "error": err.Error()}
	}
	// Aktion + Ausgang ins Recording (kind=action). Ein Artefakt reist base64
	// mit; der Orchestrator legt es als Blob ab und ersetzt es durch eine
	// Referenz (die Bytes landen nicht im JSONB).
	auditMap := map[string]any{"action": subject, "params": params, "ok": err == nil}
	if artifact != nil && len(artifact.Bytes) > 0 {
		auditMap["image_b64"] = base64.StdEncoding.EncodeToString(artifact.Bytes)
		auditMap["image_mime"] = artifact.MIME
	}
	audit, _ := json.Marshal(auditMap)
	_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
	writeJSON(w, result)
}

// httpSink schickt jeden HTTP-Request eines Plugins als event(kind=http) an die
// Control Plane — dort landet er im Request-Log. Der Aufruf darf den
// Aktions-Pfad nicht aufhalten: send() schreibt gepuffert auf die WebSocket,
// Fehler werden geschluckt (Diagnose-Daten, kein Audit-Trail). Das System aus
// der URL kennt das Plugin nicht immer — der Proxy setzt es aus dem Pfad nach.
func (p *actionProxy) httpSink(system string) reqlog.Sink {
	return func(e reqlog.Entry) {
		if e.System == "" {
			e.System = system
		}
		e.TaskID = p.taskID
		payload, err := json.Marshal(e)
		if err != nil {
			return
		}
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: EventKindHTTP, Payload: payload})
	}
}

// handleControlPlane bedient Meta-Aktionen (system="covey"), die die Control
// Plane statt eines Zielsystems betreffen: set_stage (Aufgabe auf dem Board in
// eine ggf. neue Stage schieben), add_note (Notiz an die Aufgabe), remember
// (Erkenntnis sofort ins Gedächtnis), org_chart (Organigramm abfragen),
// create_task (Teilaufgabe/Delegation) und die Wiki-Tools
// wiki_search/wiki_read/wiki_write/wiki_delete (spec/05).
func (p *actionProxy) handleControlPlane(ctx context.Context, w http.ResponseWriter, action string, params json.RawMessage) {
	switch action {
	case "create_task":
		p.handleCreateTask(ctx, w, params)
	case "org_chart":
		chart, err := p.client.orgChart(ctx)
		if err != nil {
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:org_chart"})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		writeJSON(w, map[string]any{"status": "ok", "data": chart})
	case "add_note", "remember":
		var in struct {
			Content string `json:"content"`
			TaskID  string `json:"task_id"`
		}
		if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Content) == "" {
			writeJSON(w, map[string]string{"status": "error", "error": "content fehlt"})
			return
		}
		taskID := in.TaskID
		if taskID == "" {
			taskID = p.taskID
		}
		scope := "task"
		if action == "remember" {
			scope = "memory"
		}
		if err := p.client.send(TypeNote, Note{TaskID: taskID, Scope: scope, Content: in.Content}); err != nil {
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:" + action, "scope": scope, "content": in.Content})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		writeJSON(w, map[string]string{"status": "ok", "scope": scope})
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
	case "wiki_search", "wiki_read", "wiki_write", "wiki_delete":
		var in struct {
			Query string `json:"query"`
			Slug  string `json:"slug"`
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		_ = json.Unmarshal(params, &in)
		op := strings.TrimPrefix(action, "wiki_")
		resp, err := p.client.wiki(ctx, RequestWiki{Op: op, Query: in.Query, Slug: in.Slug, Title: in.Title, Body: in.Body})
		if err != nil {
			writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
			return
		}
		if !resp.OK {
			writeJSON(w, map[string]string{"status": "error", "error": resp.Error})
			return
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:" + action, "slug": in.Slug, "query": in.Query})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		writeJSON(w, map[string]any{"status": "ok", "data": resp.Data})
	default:
		writeJSON(w, map[string]string{"status": "error", "error": fmt.Sprintf("unbekannte covey-aktion %q", action)})
	}
}

// handleCreateTask legt eine Aufgabe an: ohne "agent" eine Teilaufgabe des
// laufenden Agenten, mit "agent" eine Delegation an den Kollegen mit diesem
// Slug. Anders als die übrigen covey-Aktionen läuft sie durch die Guard-Rails —
// eine Aufgabe erzeugt Arbeit und Kosten, und Delegation (Subjekt
// covey:create_task:foreign) muss sich getrennt verbieten lassen.
func (p *actionProxy) handleCreateTask(ctx context.Context, w http.ResponseWriter, params json.RawMessage) {
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Agent    string `json:"agent"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Title) == "" {
		writeJSON(w, map[string]string{"status": "error", "error": "title fehlt"})
		return
	}
	subject := "covey:create_task"
	if strings.TrimSpace(in.Agent) != "" {
		subject += ":foreign"
	}
	dec, err := p.client.checkAction(ctx, p.taskID, subject, params)
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

	resp, err := p.client.createTask(ctx, RequestCreateTask{
		TaskID: p.taskID, Agent: in.Agent, Title: in.Title, Body: in.Body, Priority: in.Priority,
	})
	if err != nil {
		writeJSON(w, map[string]string{"status": "error", "error": err.Error()})
		return
	}
	audit, _ := json.Marshal(map[string]any{"action": subject, "params": params,
		"ok": resp.OK, "created_task": resp.TaskID, "agent": resp.Agent})
	_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
	if !resp.OK {
		writeJSON(w, map[string]string{"status": "error", "error": resp.Error})
		return
	}
	writeJSON(w, map[string]any{"status": "ok",
		"data": map[string]string{"task_id": resp.TaskID, "agent": resp.Agent}})
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
	// Workdir für Aktionen, die Dateien in die Sandbox materialisieren
	// (z. B. gitlab checkout) — das Credential selbst bleibt im Daemon.
	ctx = target.WithWorkdir(ctx, p.client.homeDir)
	return sys.Execute(ctx, action, params, target.Credential{BaseURL: cred.BaseURL, Token: cred.Token})
}
