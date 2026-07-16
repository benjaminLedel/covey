package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// Mock ist eine skriptbare Runtime für Tests und Offline-Demos. Sie wird über
// Direktiven im Aufgaben-Body gesteuert und benutzt denselben Action-Proxy wie
// eine echte Runtime — der Durchstich (Broker, Guard-Rails, Recording,
// blocked-Loop) wird damit vollständig geprüft, nur ohne LLM.
//
// Direktiven (eine pro Zeile):
//
//	[mock:action <system>/<aktion> <json-params>]  → Aktion über den Action-Proxy
//	[mock:block key=<korrelations-key> question=<text>]
//	[mock:fail <fehlertext>]
//	[mock:result <text>]
//	[mock:memory <text>]
//
// Ohne Direktiven: done mit generischem Ergebnis. Bei Resume: done mit dem
// Resume-Input als Ergebnis. Liefert der Action-Proxy pending_approval, geht
// die Mock-Runtime — wie instruiert — mit dessen correlation_key blocked.
type Mock struct{}

func (Mock) Name() string { return "mock" }

func init() {
	RegisterRuntime(RuntimeDescriptor{
		Name:            "mock",
		Label:           "Mock",
		Description:     "Skriptbare Test-Runtime ohne echten LLM — keine Kosten, keine Credentials. Für Demos und Offline-Tests.",
		NeedsCredential: false,
		New:             func() Runtime { return Mock{} },
		Setup: []SetupStep{
			{Text: "Hier beim Agenten die Runtime auf `mock` stellen."},
			{Text: "Zum Agenten wechseln (`Agenten` → Agent) und unter `Backlog` eine Aufgabe einstellen."},
			{Text: "Der Agent läuft mit skriptbarer Antwort durch — kein Credential, keine Kosten, kein `/login`-Fehler."},
		},
	})
}

var mockDirective = regexp.MustCompile(`\[mock:(action|block|fail|result|memory|sleep)\s*([^\]]*)\]`)

func (Mock) Run(ctx context.Context, spec RunSpec, onEvent func(kind string, payload json.RawMessage)) (RunResult, error) {
	res := RunResult{
		Status:       "done",
		SessionID:    "mock-session-" + spec.TaskID,
		CostUSD:      0.0123,
		InputTokens:  1000,
		OutputTokens: 200,
		Model:        "mock-model",
	}
	if spec.ResumeSessionID != "" {
		res.SessionID = spec.ResumeSessionID
	}
	emit := func(text string) {
		payload, _ := json.Marshal(map[string]string{"type": "assistant", "text": text})
		onEvent("runtime", payload)
	}
	emit("Aufgabe angenommen: " + spec.Title)
	if strings.TrimSpace(spec.MemoryContext) != "" {
		emit("Gedächtnis-Kontext liegt vor.")
	}
	if spec.ResumeSessionID != "" {
		emit("Setze Session " + spec.ResumeSessionID + " fort: " + spec.ResumeInput)
	}

	actionPort := os.Getenv("COVEY_ACTION_PORT")
	for _, env := range spec.Env {
		if v, ok := strings.CutPrefix(env, "COVEY_ACTION_PORT="); ok {
			actionPort = v
		}
	}

	for _, m := range mockDirective.FindAllStringSubmatch(spec.Body, -1) {
		kind, arg := m[1], strings.TrimSpace(m[2])
		switch kind {
		case "action":
			path, params, _ := strings.Cut(arg, " ")
			if params == "" {
				params = "{}"
			}
			status, body, err := callActionProxy(ctx, actionPort, path, params)
			if err != nil {
				res.Status = "failed"
				res.Error = fmt.Sprintf("action %s: %v", path, err)
				return res, nil
			}
			emit(fmt.Sprintf("Aktion %s → %s", path, status))
			if status == "pending_approval" {
				// Wie im Plattform-Protokoll: auf die Freigabe blocken.
				var pending struct {
					CorrelationKey string `json:"correlation_key"`
				}
				json.Unmarshal(body, &pending)
				res.Status = "blocked"
				res.CorrelationKey = pending.CorrelationKey
				res.Question = "Warte auf menschliche Freigabe für " + path
				return res, nil
			}
			if status == "denied" {
				emit("Aktion durch Guard-Rail verboten, wähle anderen Weg.")
			}
		case "block":
			if spec.ResumeSessionID != "" {
				continue // beim Resume nicht erneut blocken
			}
			res.Status = "blocked"
			res.CorrelationKey = mockKV(arg, "key")
			res.Question = mockKV(arg, "question")
			return res, nil
		case "fail":
			res.Status = "failed"
			res.Error = arg
			return res, nil
		case "sleep":
			d, err := time.ParseDuration(arg)
			if err != nil {
				d = time.Second
			}
			select {
			case <-ctx.Done():
				return res, ctx.Err()
			case <-time.After(d):
			}
		case "result":
			res.Result = arg
		case "memory":
			res.Memory = arg
		}
	}
	if res.Result == "" {
		if spec.ResumeSessionID != "" {
			res.Result = "Fortgesetzt und abgeschlossen: " + spec.ResumeInput
		} else {
			res.Result = "Erledigt: " + spec.Title
		}
	}
	return res, nil
}

// mockKV liest key=... question=... aus einer Direktive (question schluckt den Rest).
func mockKV(s, key string) string {
	idx := strings.Index(s, key+"=")
	if idx < 0 {
		return ""
	}
	v := s[idx+len(key)+1:]
	if key != "question" {
		if sp := strings.IndexByte(v, ' '); sp >= 0 {
			v = v[:sp]
		}
	}
	return strings.TrimSpace(v)
}

func callActionProxy(ctx context.Context, port, path, params string) (string, []byte, error) {
	if port == "" {
		return "", nil, fmt.Errorf("kein COVEY_ACTION_PORT gesetzt")
	}
	url := "http://127.0.0.1:" + port + "/actions/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte(params)))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Status string `json:"status"`
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		return "", nil, fmt.Errorf("proxy-antwort: %w", err)
	}
	return out.Status, buf.Bytes(), nil
}
