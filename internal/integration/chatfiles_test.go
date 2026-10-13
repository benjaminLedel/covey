package integration

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
)

// pngBytes is the head of a PNG — enough for a type to be read off it.
var pngBytes = append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{7}, 64)...)

// chatForm sends a message with files as multipart/form-data.
func chatForm(t *testing.T, c *apiClient, path, text string, files map[string][]byte, order ...string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if text != "" {
		_ = mw.WriteField("text", text)
	}
	for _, name := range order {
		part, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(files[name])
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// holeAnhang reads a route's status, headers and body.
func holeAnhang(t *testing.T, c *apiClient, path string) (int, http.Header, []byte) {
	t.Helper()
	resp := c.do(http.MethodGet, path, nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

// TestAgentHandsAFileInChat checks the agent's half of #586: covey/message
// with files carries them out of the sandbox into the conversation, the
// person fetches them — an image shown in place, any file as a download —
// and nobody outside the conversation does.
func TestAgentHandsAFileInChat(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("grafikerin")
	if err := s.registry.SetSupervisor(ctx, agent.ID, &s.adminID); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(s.homeBase, agent.ID.String(), "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "post.png"), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "bericht.txt"), []byte("Zahlen."), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Bild liefern",
		`[mock:action covey/message {"text":"Der Entwurf.","files":["~/out/post.png","out/bericht.txt"]}]
[mock:result geliefert]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool { return s.taskState(task.ID) == backlog.StateDone })

	store := chat.New(s.pool)
	convID, ok, err := store.FindDirect(ctx, s.orgID, chat.Agent(agent.ID), chat.Human(s.adminID))
	if err != nil || !ok {
		t.Fatalf("the conversation must exist: ok=%v err=%v", ok, err)
	}
	msgs, err := store.Page(ctx, convID, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found *chat.Message
	for i := range msgs {
		if msgs[i].Text == "Der Entwurf." {
			found = &msgs[i]
		}
	}
	if found == nil || len(found.Attachments) != 2 {
		t.Fatalf("the message must carry both files, got %+v", found)
	}
	byName := map[string]chat.Attachment{}
	for _, a := range found.Attachments {
		byName[a.Name] = a
	}
	img, ok := byName["post.png"]
	if !ok || img.MIME != "image/png" || img.Size != int64(len(pngBytes)) {
		t.Fatalf("the image as it was sent: %+v", found.Attachments)
	}
	txt := byName["bericht.txt"]

	admin := teamLogin(t, s)
	// The thread the web reads shows the files at the message.
	verlauf := admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/thread", nil, http.StatusOK)
	var gesehen bool
	for _, roh := range verlauf["entries"].([]any) {
		e := roh.(map[string]any)
		if as, _ := e["attachments"].([]any); len(as) == 2 {
			gesehen = true
		}
	}
	if !gesehen {
		t.Fatalf("the thread must show the attachments: %v", verlauf["entries"])
	}

	code, h, body := holeAnhang(t, admin, "/api/v1/chat/attachments/"+img.ID.String())
	if code != http.StatusOK || h.Get("Content-Type") != "image/png" || !bytes.Equal(body, pngBytes) {
		t.Fatalf("the image is shown in place: %d %q (%d bytes)", code, h.Get("Content-Type"), len(body))
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("an inline attachment carries the preview's headers: %v", h)
	}
	code, h, body = holeAnhang(t, admin, "/api/v1/chat/attachments/"+txt.ID.String())
	if code != http.StatusOK || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") || string(body) != "Zahlen." {
		t.Fatalf("any other file is a download: %d %q %q", code, h.Get("Content-Disposition"), body)
	}

	// Somebody outside the conversation gets the 404 of an unknown id; the
	// audit, which reads every conversation, gets the file.
	s.mitglied(t, "fremd@test.local", "Fremd", "agent_owner", "fremd-passwort-123")
	fremd := login(t, s, "fremd@test.local", "fremd-passwort-123")
	if code, _, _ := holeAnhang(t, fremd, "/api/v1/chat/attachments/"+img.ID.String()); code != http.StatusNotFound {
		t.Fatalf("a non-member must not read the file, got %d", code)
	}
	s.mitglied(t, "pruefer@test.local", "Prüfer", "auditor", "pruefer-passwort-123")
	pruefer := login(t, s, "pruefer@test.local", "pruefer-passwort-123")
	if code, _, _ := holeAnhang(t, pruefer, "/api/v1/chat/attachments/"+img.ID.String()); code != http.StatusOK {
		t.Fatalf("the audit reads the file, got %d", code)
	}
	if code, _, _ := holeAnhang(t, admin, "/api/v1/chat/attachments/"+uuid.NewString()); code != http.StatusNotFound {
		t.Fatalf("an unknown id is a 404, got %d", code)
	}

	// Over the limit: refused in the sandbox, and nothing is written.
	big := make([]byte, chat.MaxAttachmentBytes+1)
	if err := os.WriteFile(filepath.Join(out, "film.mp4"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	zuGross, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Film liefern",
		`[mock:action covey/message {"text":"Der Film.","files":["~/out/film.mp4"]}]
[mock:result geliefert]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(zuGross.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	if e := taskError(t, s, zuGross.ID); !strings.Contains(e, "too large") {
		t.Fatalf("the refusal must name the limit, error is %q", e)
	}
	msgs, _ = store.Page(ctx, convID, nil, 20)
	for _, m := range msgs {
		if m.Text == "Der Film." {
			t.Fatalf("a refused message must not stand in the conversation")
		}
	}
}

// TestPersonAttachesFilesForAgent checks the person's half of #586: files
// sent with a message are stored with it, put into the agent's home under
// chat/, and the task made of the message names where they lie. A seat
// without the manage role can do it — the Files tab's upload is not the way.
func TestPersonAttachesFilesForAgent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("leserin")
	s.ohneLaeufe(agent.ID)
	teamLogin(t, s)
	s.mitglied(t, "kasse@test.local", "Kasse", "controlling", "kasse-passwort-123")
	kasse := login(t, s, "kasse@test.local", "kasse-passwort-123")
	base := "/api/v1/agents/" + agent.ID.String()

	resp := chatForm(t, kasse, base+"/messages", "Bitte die Rechnungen lesen.",
		map[string][]byte{"rechnung.txt": []byte("Summe 42"), "RECHNUNG.txt": []byte("Summe 7")}, "rechnung.txt", "RECHNUNG.txt")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("a message with files: HTTP %d %s", resp.StatusCode, body)
	}

	store := chat.New(s.pool)
	convID, ok, err := store.FindDirect(ctx, s.orgID, chat.Agent(agent.ID), chat.Human(humanID(t, s, "kasse@test.local")))
	if err != nil || !ok {
		t.Fatalf("the conversation must exist: %v %v", ok, err)
	}
	msgs, err := store.Page(ctx, convID, nil, 5)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("one message: %v %v", msgs, err)
	}
	m := msgs[0]
	if m.Text != "Bitte die Rechnungen lesen." || len(m.Attachments) != 2 {
		t.Fatalf("the message keeps what was said, and the files beside it: %+v", m)
	}
	// Two names that differ only in case are two files, not one replacing
	// the other.
	if m.Attachments[0].HomePath == m.Attachments[1].HomePath ||
		!strings.EqualFold(filepath.Base(m.Attachments[1].HomePath), "RECHNUNG (2).txt") {
		t.Fatalf("each file its own name: %+v", m.Attachments)
	}
	for _, a := range m.Attachments {
		if !strings.HasPrefix(a.HomePath, "chat/"+m.ID.String()[:8]+"/") {
			t.Fatalf("the file lies under chat/<message>/: %q", a.HomePath)
		}
		got, err := os.ReadFile(filepath.Join(s.homeBase, agent.ID.String(), a.HomePath))
		if err != nil {
			t.Fatalf("the file must be in the agent's home: %v", err)
		}
		if len(got) == 0 {
			t.Fatalf("the file in the home is empty")
		}
	}
	if m.TaskID == nil {
		t.Fatalf("the message must have become a task")
	}
	task, err := s.backlog.Get(ctx, *m.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(task.Body, m.Attachments[0].HomePath) || !strings.Contains(task.Body, m.Attachments[1].HomePath) {
		t.Fatalf("the task must name where the files lie: %q", task.Body)
	}

	// The sender reads the file back from the conversation.
	if code, _, got := holeAnhang(t, kasse, "/api/v1/chat/attachments/"+m.Attachments[0].ID.String()); code != http.StatusOK || string(got) != "Summe 42" {
		t.Fatalf("the sender fetches the file: %d %q", code, got)
	}

	// Files alone are a message; nothing at all is not.
	resp = chatForm(t, kasse, base+"/messages", "", map[string][]byte{"nur.png": pngBytes}, "nur.png")
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("files without text are a message, got %d", resp.StatusCode)
	}
	resp = chatForm(t, kasse, base+"/messages", "", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("an empty form is refused, got %d", resp.StatusCode)
	}
	// Over the limit: refused, and nothing is written.
	resp = chatForm(t, kasse, base+"/messages", "Zu viel.", map[string][]byte{"gross.bin": make([]byte, chat.MaxAttachmentBytes+1)}, "gross.bin")
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("over the limit is a 413, got %d", resp.StatusCode)
	}
	msgs, _ = store.Page(ctx, convID, nil, 10)
	for _, x := range msgs {
		if x.Text == "Zu viel." {
			t.Fatalf("a refused message must not stand in the conversation")
		}
	}

	// The conversation route takes the same form.
	resp = chatForm(t, kasse, "/api/v1/conversations/"+convID.String()+"/messages", "Noch eine.",
		map[string][]byte{"noch.txt": []byte("x")}, "noch.txt")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || !strings.Contains(string(body), `"home_path":"chat/`) {
		t.Fatalf("the conversation route: HTTP %d %s", resp.StatusCode, body)
	}
}

// humanID reads a person's id by e-mail.
func humanID(t *testing.T, s *stack, email string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM humans WHERE email = $1`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
