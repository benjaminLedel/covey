package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"covey/internal/backlog"
)

// TestChatIsADoorIntoTheBacklog checks the one claim the chat makes: it is not
// a second orchestration path. A message is a task, a reply is the resume input
// of a parked task, and the thread reads what the objects already carry.
//
// The path in full, because that is where it would break: message → task with
// origin `chat:` → the agent parks with a question → the thread shows the
// question without its state prefix → the reply wakes the task with the text as
// resume input.
func TestChatIsADoorIntoTheBacklog(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// Paused: the task is meant to stay where the test puts it and not be
	// picked up by the dispatcher in the middle of the run.
	agent := s.newSupportAgent("chat-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}

	admin := login(t, s, "admin@test.local", "admin-passwort")
	base := "/api/v1/agents/" + agent.ID.String()

	// 1. The message becomes a task. The first line is the title, the whole
	//    text stays in the body, and the origin says where it came from.
	msg := "Bitte die Rechnung von Globex prüfen\nSie liegt seit gestern im Postfach."
	created := admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": msg}, http.StatusCreated)
	if got := created["title"]; got != "Bitte die Rechnung von Globex prüfen" {
		t.Fatalf("title: %v", got)
	}
	if got := created["origin"]; got != "chat:admin@test.local" {
		t.Fatalf("origin: %v", got)
	}
	taskID := created["id"].(string)

	// An empty message is not an empty task.
	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "   "}, http.StatusBadRequest)

	// 2. The agent takes it and parks it with a question — the ordinary
	//    blocked edge, not a chat-specific one.
	task, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID.String() != taskID {
		t.Fatalf("the dispatcher picked up something else: %s", task.ID)
	}
	if _, err := s.backlog.Block(ctx, task.ID, "chat-frage", "sitzung-1", "Darf ich Globex direkt antworten?"); err != nil {
		t.Fatal(err)
	}

	// 3. The thread shows the message and the question — and the question
	//    without the "blocked:" its transition note was written with.
	thread := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	entries, _ := thread["entries"].([]any)
	kinds := map[string]string{}
	for _, e := range entries {
		m := e.(map[string]any)
		kinds[m["kind"].(string)] = m["text"].(string)
	}
	// Der Text der Nachricht, nicht Titel plus Text: Die erste Zeile IST der
	// Titel, und zusammengeklebt stünde jede Nachricht doppelt im Verlauf.
	if got, ok := kinds["message"]; !ok || got != msg {
		t.Fatalf("message in thread: %q", got)
	}
	if got := kinds["question"]; got != "Darf ich Globex direkt antworten?" {
		t.Fatalf("question in thread: %q", got)
	}

	// 4. The reply wakes it: the text is the resume input, the task stands
	//    open again, and the reply is in the record as a note.
	reply := admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/reply",
		map[string]any{"text": "Ja, aber ohne Preisnachlass."}, http.StatusOK)
	if reply["woken"] != true {
		t.Fatalf("a blocked task has to be woken by the reply: %v", reply)
	}
	after, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != backlog.StateOpen {
		t.Fatalf("state after the reply: %s", after.State)
	}
	if after.ResumeInput == nil || *after.ResumeInput != "Ja, aber ohne Preisnachlass." {
		t.Fatalf("resume input: %v", after.ResumeInput)
	}

	// 5. A second reply finds nobody waiting. It is still written down —
	//    that is the difference between "no" and "lost".
	again := admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/reply",
		map[string]any{"text": "Nachtrag: die Adresse hat sich geändert."}, http.StatusOK)
	if again["woken"] != false {
		t.Fatalf("nobody was waiting, so nothing may be woken: %v", again)
	}
	notes := admin.expectList(http.MethodGet, "/api/v1/tasks/"+taskID+"/notes", nil, http.StatusOK)
	if len(notes) != 2 {
		t.Fatalf("both replies belong in the record, got %d", len(notes))
	}
	if got := notes[0]["author"]; got != "human:admin@test.local" {
		t.Fatalf("author of the reply: %v", got)
	}
}

// TestReplyOnlyWakesATaskThatWaits holds the guard that `Answer` needs and the
// state machine does not give it. `open` is reachable from four states, so a
// reply that only says "back to open" would also take a task that is RUNNING —
// the dispatcher picks it up a second time, and the run already in flight loses
// its result the moment it reports done against a task that no longer stands in
// progress. The review of #298 reproduced exactly that.
//
// A reply to such a task is therefore written down and wakes nobody.
func TestReplyOnlyWakesATaskThatWaits(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("reply-guard")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	reply := func(id string) map[string]any {
		t.Helper()
		return admin.expect(http.MethodPost, "/api/v1/tasks/"+id+"/reply",
			map[string]any{"text": "Nachtrag"}, http.StatusOK)
	}

	// Running: the reply must not touch the state, or the run loses its result.
	laufend, err := s.backlog.Create(ctx, s.orgID, agent.ID, "läuft gerade", "", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if got := reply(laufend.ID.String())["woken"]; got != false {
		t.Fatalf("a running task may not be woken: %v", got)
	}
	nach, err := s.backlog.Get(ctx, laufend.ID)
	if err != nil {
		t.Fatal(err)
	}
	if nach.State != backlog.StateInProgress {
		t.Fatalf("the run was torn out from under itself: %s", nach.State)
	}
	// And it can still finish — that is the half that was actually lost.
	if _, err := s.backlog.Complete(ctx, laufend.ID, backlog.StateDone, "fertig", ""); err != nil {
		t.Fatalf("the finished run could not report its result: %v", err)
	}

	// Failed and cancelled: likewise nobody is waiting there.
	fehl, err := s.backlog.Create(ctx, s.orgID, agent.ID, "schlug fehl", "", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, fehl.ID, backlog.StateFailed, "", "boom"); err != nil {
		t.Fatal(err)
	}
	if got := reply(fehl.ID.String())["woken"]; got != false {
		t.Fatalf("a failed task may not be restarted by a reply: %v", got)
	}
	wieder, err := s.backlog.Get(ctx, fehl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if wieder.State != backlog.StateFailed {
		t.Fatalf("state of the failed task after the reply: %s", wieder.State)
	}
}

// TestAMessageInAScriptWithoutSpacesSurvivesItsTitle is the byte-versus-character
// cut. A long Japanese sentence has no spaces and three bytes per character; cut
// at byte 80, the title ends inside a character, Postgres refuses the invalid
// UTF-8 (22021) and the message is answered with a 500. This interface ships in
// ten languages, two of which write that way in an ordinary sentence.
func TestAMessageInAScriptWithoutSpacesSurvivesItsTitle(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("titel-schnitt")
	admin := login(t, s, "admin@test.local", "admin-passwort")

	lang := strings.Repeat("請求書の確認をお願いします", 12) // no space, 3 bytes per character
	created := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": lang}, http.StatusCreated)

	titel, _ := created["title"].(string)
	if !utf8.ValidString(titel) {
		t.Fatalf("the title is not valid UTF-8: %q", titel)
	}
	if n := utf8.RuneCountInString(titel); n > 81 { // 80 plus the ellipsis
		t.Fatalf("the title was not shortened: %d characters", n)
	}
	// The body keeps the whole message — only the title is cut.
	if created["body"] != lang {
		t.Fatal("the body must carry the message unabridged")
	}
}

// TestABodyNobodyCanReadSaysSo keeps two mistakes apart that answered the same
// sentence: a body that is not the expected JSON, and an empty message.
func TestABodyNobodyCanReadSaysSo(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("rumpf")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	pfad := "/api/v1/agents/" + agent.ID.String() + "/messages"

	kaputt := admin.doRaw(http.MethodPost, pfad, "{nicht wirklich json")
	if kaputt.StatusCode != http.StatusBadRequest {
		t.Fatalf("status for a broken body: %d", kaputt.StatusCode)
	}
	leer := admin.expect(http.MethodPost, pfad, map[string]any{"text": "  "}, http.StatusBadRequest)
	if leer["error"] != "text is required" {
		t.Fatalf("an empty message deserves its own sentence: %v", leer)
	}
}
