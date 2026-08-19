// Command demoplugin is a target-system plugin written as ordinary Go and
// compiled to WebAssembly:
//
//	GOOS=wasip1 GOARCH=wasm go build -o demo.wasm .
//
// It is the fixture the runtime is tested against, and at the same time the
// smallest complete example of the protocol: read one invocation from stdin,
// write messages to stdout, end with a result or an error.
//
// Note what it never does and never can: open a socket, read a file, or touch
// a credential. It asks the host to fetch, and the host adds the base URL and
// the token.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type invocation struct {
	Op     string          `json:"op"`
	Action string          `json:"action"`
	Params json.RawMessage `json:"params"`
	Kind   string          `json:"kind"`
	Scopes []string        `json:"scopes"`
	Body   json.RawMessage `json:"body"`
}

type fetchReq struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
}

type fetchResp struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body"`
	Text   string          `json:"text"`
	Error  string          `json:"error"`
}

var in *bufio.Reader

func main() {
	in = bufio.NewReaderSize(os.Stdin, 1<<20)
	line, err := in.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		fail("no invocation on stdin")
		return
	}
	var inv invocation
	if err := json.Unmarshal(line, &inv); err != nil {
		fail("invocation is not JSON: " + err.Error())
		return
	}

	switch inv.Op {
	case "describe":
		emit(map[string]any{"describe": map[string]any{
			"name":        "demo",
			"label":       "Demo (wasm)",
			"description": "Fixture plugin: proves that compiled code can be a target system.",
			"category":    "dev",
			"scopes":      []string{"read", "write"},
			"probe":       true,
			"poll":        true,
			// Declared, so an operator sees before installing that this module
			// reads out of the agent's checkout.
			"workdir": true,
			// The webhook entrance. The module says only how the host is to
			// check the signature — it never sees the secret.
			"webhook": map[string]any{"signature": "hmac-sha256"},
			"actions": []map[string]any{
				{"name": "get_issue", "doc": "read one issue; params: id", "scope": "read"},
				{"name": "comment", "doc": "write a comment; params: id, body", "scope": "write",
					"subject": "comment_external"},
				{"name": "shout", "doc": "uppercase a string locally; params: text", "scope": "read"},
				{"name": "read_lock", "doc": "read a declared dependency file; params: path", "scope": "read"},
			},
		}})
	case "probe":
		resp := fetch(fetchReq{Method: "GET", Path: "/me"})
		if resp.Error != "" || resp.Status != 200 {
			fail("probe failed: " + firstNonEmpty(resp.Error, fmt.Sprintf("HTTP %d", resp.Status)))
			return
		}
		var me struct {
			Login string `json:"login"`
		}
		json.Unmarshal(resp.Body, &me)
		result(me.Login)
	case "poll":
		path := "/issues?state=open"
		if inv.Kind != "" {
			path = "/issues?state=open&kind=" + inv.Kind
		}
		resp := fetch(fetchReq{Method: "GET", Path: path})
		if resp.Error != "" {
			fail(resp.Error)
			return
		}
		var items []struct {
			ID        int    `json:"id"`
			UpdatedAt string `json:"updated_at"`
		}
		json.Unmarshal(resp.Body, &items)
		sig := make([]string, 0, len(items))
		for _, it := range items {
			sig = append(sig, fmt.Sprintf("%d@%s", it.ID, it.UpdatedAt))
		}
		emit(map[string]any{"result": map[string]any{
			"has_work": len(items) > 0, "signature": strings.Join(sig, ","),
		}})
	case "webhook":
		webhook(inv.Body)
	case "prompt_doc":
		doc := "Demo actions: get_issue, shout"
		for _, s := range inv.Scopes {
			if s == "write" {
				doc += ", comment"
			}
		}
		result(doc)
	case "execute":
		execute(inv)
	default:
		fail("unknown op " + inv.Op)
	}
}

// webhook turns a verified payload into a backlog event. This is the half a
// manifest cannot do: whether something is news or the echo of the agent's own
// comment is a decision, not a field.
func webhook(body []byte) {
	var p struct {
		Issue struct {
			ID    int    `json:"id"`
			Title string `json:"title"`
		} `json:"issue"`
		Comment struct {
			ID     int    `json:"id"`
			Body   string `json:"body"`
			Author string `json:"author"`
		} `json:"comment"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		fail("payload is not JSON: " + err.Error())
		return
	}
	emit(map[string]any{"event": map[string]any{
		"dedup_key":       fmt.Sprintf("demo:comment:%d", p.Comment.ID),
		"correlation_key": fmt.Sprintf("demo:issue:%d", p.Issue.ID),
		"title":           "Demo issue #" + fmt.Sprint(p.Issue.ID) + ": " + p.Issue.Title,
		"task_body":       "New comment on issue " + fmt.Sprint(p.Issue.ID) + ":\n" + p.Comment.Body,
		"resume_input":    p.Comment.Body,
		// The agent's own comment is registered for dedup and wakes nobody.
		"wake": p.Comment.Author != "covey-agent",
	}})
}

func execute(inv invocation) {
	var p struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		Text string `json:"text"`
		Path string `json:"path"`
	}
	json.Unmarshal(inv.Params, &p)

	switch inv.Action {
	case "read_lock":
		// The case the workspace read exists for: judge what a project
		// declares, without the plugin having a filesystem of its own.
		resp := readFile(p.Path)
		if resp.Error != "" {
			fail(resp.Error)
			return
		}
		result(map[string]any{"path": p.Path, "bytes": len(resp.Text), "text": resp.Text})
	case "shout":
		// Real computation, no call: the thing a manifest cannot do.
		result(strings.ToUpper(p.Text))
	case "now":
		// The clock, so a test can prove the module has one. wazero's default
		// is frozen at 2022-01-01; the host grants the real one deliberately
		// (see the ModuleConfig in runtime.go), and a plugin that writes a
		// timestamp somebody else reads depends on that grant.
		result(time.Now().UTC().Format(time.RFC3339))
	case "get_issue":
		resp := fetch(fetchReq{Method: "GET", Path: fmt.Sprintf("/issues/%d", p.ID)})
		if resp.Error != "" {
			fail(resp.Error)
			return
		}
		emit(map[string]any{"result": json.RawMessage(resp.Body)})
	case "comment":
		body, _ := json.Marshal(map[string]string{"body": p.Body})
		resp := fetch(fetchReq{Method: "POST", Path: fmt.Sprintf("/issues/%d/comments", p.ID), Body: body})
		if resp.Status >= 300 || resp.Error != "" {
			fail("comment rejected: " + firstNonEmpty(resp.Error, fmt.Sprintf("HTTP %d", resp.Status)))
			return
		}
		result("commented")
	default:
		fail("unknown action " + inv.Action)
	}
}

// fetch asks the host for one request and waits for its answer.
func fetch(req fetchReq) fetchResp {
	emit(map[string]any{"fetch": req})
	line, err := in.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return fetchResp{Error: "host closed the connection"}
	}
	var resp fetchResp
	if err := json.Unmarshal(line, &resp); err != nil {
		return fetchResp{Error: "host answer is not JSON: " + err.Error()}
	}
	return resp
}

type readFileResp struct {
	Text  string `json:"text"`
	Error string `json:"error"`
}

// readFile asks the host for one file out of the workspace. Same shape as
// fetch: the module names what it wants, the host decides whether it gets it.
func readFile(path string) readFileResp {
	emit(map[string]any{"read_file": map[string]any{"path": path}})
	line, err := in.ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return readFileResp{Error: "host closed the connection"}
	}
	var resp readFileResp
	if err := json.Unmarshal(line, &resp); err != nil {
		return readFileResp{Error: "host answer is not JSON: " + err.Error()}
	}
	return resp
}

func emit(v any) {
	b, _ := json.Marshal(v)
	os.Stdout.Write(append(b, '\n'))
}

func result(v any) {
	b, _ := json.Marshal(v)
	emit(map[string]any{"result": json.RawMessage(b)})
}

func fail(msg string) { emit(map[string]any{"error": msg}) }

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
