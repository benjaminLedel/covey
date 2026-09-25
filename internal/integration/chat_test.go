package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

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

	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	/* 1. Die Nachricht wird geschrieben, und ohne Triage wird eine Aufgabe
	   daraus: erste Zeile als Titel, der ganze Text als Rumpf, die Herkunft
	   sagt, woher er kam. Die Antwort trägt beides — die Nachricht ist das
	   Gesagte, die Aufgabe das Daraus-Gewordene (#302). */
	msg := "Bitte die Rechnung von Globex prüfen\nSie liegt seit gestern im Postfach."
	created := admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": msg}, http.StatusCreated)
	if created["answered"] != false {
		t.Fatalf("without triage nothing is answered: %v", created["answered"])
	}
	nachricht, _ := created["message"].(map[string]any)
	if nachricht["text"] != msg || nachricht["author"] != "chat:admin@test.local" {
		t.Fatalf("the message as it was said: %v", nachricht)
	}
	aufgabe, _ := created["task"].(map[string]any)
	if got := aufgabe["title"]; got != "Bitte die Rechnung von Globex prüfen" {
		t.Fatalf("title: %v", got)
	}
	if got := aufgabe["origin"]; got != "chat:admin@test.local" {
		t.Fatalf("origin: %v", got)
	}
	taskID := aufgabe["id"].(string)
	if nachricht["task_id"] != taskID {
		t.Fatalf("the message has to point at the task it became: %v", nachricht["task_id"])
	}

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
	admin := teamLogin(t, s)

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
	admin := teamLogin(t, s)

	lang := strings.Repeat("請求書の確認をお願いします", 12) // no space, 3 bytes per character
	created := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": lang}, http.StatusCreated)

	aufgabe, _ := created["task"].(map[string]any)
	titel, _ := aufgabe["title"].(string)
	if !utf8.ValidString(titel) {
		t.Fatalf("the title is not valid UTF-8: %q", titel)
	}
	if n := utf8.RuneCountInString(titel); n > 81 { // 80 plus the ellipsis
		t.Fatalf("the title was not shortened: %d characters", n)
	}
	// The body keeps the whole message — only the title is cut.
	if aufgabe["body"] != lang {
		t.Fatal("the body must carry the message unabridged")
	}
}

// TestABodyNobodyCanReadSaysSo keeps two mistakes apart that answered the same
// sentence: a body that is not the expected JSON, and an empty message.
func TestABodyNobodyCanReadSaysSo(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("rumpf")
	admin := teamLogin(t, s)
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

// TestAReactionIsAToggleAndBelongsToTheTask holds the two decisions behind
// reactions: the same click takes the mark back, and the mark hangs on the
// TASK, not on a line of the thread — a thread's lines are derived, and only
// the task has an identifier that is the same tomorrow.
func TestAReactionIsAToggleAndBelongsToTheTask(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("marken")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	created := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Bitte den Mahnlauf prüfen"}, http.StatusCreated)
	taskID := created["task"].(map[string]any)["id"].(string)
	pfad := "/api/v1/tasks/" + taskID + "/reactions"

	// Setzen, und im Verlauf steht es.
	if got := admin.expect(http.MethodPost, pfad, map[string]any{"emoji": "👀"}, http.StatusOK)["set"]; got != true {
		t.Fatalf("the first click sets the mark: %v", got)
	}
	marken := func() []any {
		t.Helper()
		thread := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
		alle, _ := thread["marks"].(map[string]any)
		liste, _ := alle[taskID].([]any)
		return liste
	}
	if len(marken()) != 1 {
		t.Fatalf("the mark belongs in the thread: %v", marken())
	}
	erste := marken()[0].(map[string]any)
	if erste["emoji"] != "👀" || erste["count"] != float64(1) || erste["mine"] != true {
		t.Fatalf("grouped mark: %v", erste)
	}

	// Derselbe Klick nimmt sie zurück — kein zweiter Endpunkt dafür.
	if got := admin.expect(http.MethodPost, pfad, map[string]any{"emoji": "👀"}, http.StatusOK)["set"]; got != false {
		t.Fatalf("the second click takes it back: %v", got)
	}
	if len(marken()) != 0 {
		t.Fatalf("after taking it back nothing stands there: %v", marken())
	}

	// Zwei Verfasser, ein Zeichen: eine Marke, zweimal gezählt.
	if _, err := s.backlog.React(ctx, mussUUID(t, taskID), "👍", "agent"); err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, pfad, map[string]any{"emoji": "👍"}, http.StatusOK)
	if m := marken()[0].(map[string]any); m["count"] != float64(2) || m["mine"] != true {
		t.Fatalf("two authors, one mark: %v", m)
	}

	// Ein Satz ist keine Reaktion.
	admin.expect(http.MethodPost, pfad, map[string]any{"emoji": "sieht gut aus"}, http.StatusBadRequest)

	// Und die Platte, die der Agent beim Annehmen setzt, ist kein Umschalter:
	// ein zweiter Lauf derselben Aufgabe darf sie nicht wieder entfernen.
	id := mussUUID(t, taskID)
	for i := 0; i < 2; i++ {
		if err := s.backlog.MarkReaction(ctx, id, "🎉", "agent"); err != nil {
			t.Fatal(err)
		}
	}
	roh, err := s.backlog.ReactionsByTasks(ctx, []uuid.UUID{id})
	if err != nil {
		t.Fatal(err)
	}
	var feiern int
	for _, r := range roh[id] {
		if r.Emoji == "🎉" {
			feiern++
		}
	}
	if feiern != 1 {
		t.Fatalf("marking twice stays one mark, got %d", feiern)
	}
}

func mussUUID(t *testing.T, s string) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

/*
TestChatAcceptsWithoutWaitingForTheTriage prüft die Form, die #304 gebracht

	hat: Das Abschicken wartet nicht auf das Modell.

*
* Es gibt in diesem Stapel keinen Anbieter, und genau das macht den Test
* scharf: Die Triage ist eingeschaltet, sie scheitert an der fehlenden
* Zugangsdatei, und trotzdem muss dabei ALLES stimmen — 202 sofort, die
* Nachricht steht schon im Verlauf, `pending` sagt, dass noch etwas kommt,
* und kurz darauf ist aus ihr eine Aufgabe geworden. Eine Nachricht darf nie
* verschwinden, auch nicht, wenn nichts antwortet.
*/
func TestChatAcceptsWithoutWaitingForTheTriage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("chat-async")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	angenommen := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Bitte die Rechnung von Globex prüfen"}, http.StatusAccepted)
	if angenommen["pending"] != true {
		t.Fatalf("an accepted message says that a decision is still owed: %v", angenommen)
	}
	if angenommen["task"] != nil {
		t.Fatalf("the task does not exist yet — claiming it does is the old, blocking shape: %v", angenommen["task"])
	}
	nachricht, _ := angenommen["message"].(map[string]any)
	if nachricht["text"] != "Bitte die Rechnung von Globex prüfen" {
		t.Fatalf("the message is written before the decision: %v", nachricht)
	}

	/* Und dann kommt die Entscheidung nach. Ohne Anbieter ist sie „Aufgabe" —
	   das Verhalten von vor der Triage, das jeder Fehlerfall wiederherstellt. */
	wartenAuf(t, "the message becomes a task", func() bool {
		tasks, err := s.backlog.ListByAgent(ctx, agent.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		return len(tasks) == 1 && tasks[0].Origin == "chat:admin@test.local"
	})

	/* Der Verlauf sagt am Ende nicht mehr, dass etwas aussteht. Solange er es
	   sagt, steht in der Oberfläche die Blase „denkt nach" — und eine, die
	   nicht wieder verschwindet, ist schlimmer als gar keine. */
	wartenAuf(t, "the thread stops saying that something is pending", func() bool {
		verlauf := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
		return verlauf["pending"] == false
	})
}

/* TestChatTriageSurvivesARestart prüft das Netz unter der Goroutine.
 *
 * Die Nachricht ist angenommen, der Zug läuft daneben — stirbt die Control
 * Plane dazwischen, stünde sie für immer auf `pending`: angenommen, aber ohne
 * Antwort und ohne Aufgabe. Genau dafür steht der Zustand in der Datenbank
 * und nicht in einer Goroutine.
 *
 * Der Neustart wird hier nachgestellt, indem eine Nachricht von Hand als
 * `pending` eingetragen und dann das Aufräumen aufgerufen wird — das ist
 * derselbe Weg, den `covey serve` beim Hochfahren geht.
 */
func TestChatTriageSurvivesARestart(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("chat-restart")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}

	id := uuid.New()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO chat_messages (id, org_id, agent_id, author, text, triage_state)
		 VALUES ($1,$2,$3,'chat:admin@test.local','Die Wartung von morgen absagen','pending')`,
		id, s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}

	s.srv.NachholenOffeneTriage(ctx)

	wartenAuf(t, "the left-over message becomes a task", func() bool {
		tasks, err := s.backlog.ListByAgent(ctx, agent.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		return len(tasks) == 1 && tasks[0].Title == "Die Wartung von morgen absagen"
	})

	/* Und sie hängt an der Nachricht: Der Verlauf zeigt das Gesagte und das
	   Daraus-Gewordene als eine Zeile, nicht als zwei. */
	var taskID *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT task_id FROM chat_messages WHERE id=$1`, id).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if taskID == nil {
		t.Fatal("the caught-up message has to point at the task it became")
	}
}

// wartenAuf pollt eine Bedingung, die eine Goroutine erfüllt. Zwei Sekunden
// sind großzügig für eine Einfügung und knapp genug, dass ein Fehlschlag den
// Lauf nicht aufhält.
func wartenAuf(t *testing.T, was string, erfuellt func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if erfuellt() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", was)
}

/*
TestThreadCarriesItsSearchAndItsBackgroundWork prüft die zwei Auskünfte, die

	der Verlauf seit #308/#309 zusätzlich gibt.

*
* Beide sind aus demselben Grund nötig: Der Verlauf zeigt, was GESAGT wurde,
* und ein Vorgang, der seit einer Stunde läuft, hat seit einer Stunde nichts
* gesagt. Er braucht deshalb eine eigene Anzeige — und einen Weg, ihn
* wiederzufinden, wenn er aus dem Fenster gelaufen ist.
*/
func TestThreadCarriesItsSearchAndItsBackgroundWork(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("chat-suche")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Bitte die Rechnung von Globex prüfen"}, http.StatusCreated)
	admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Und den Urlaubsantrag von Meyer freigeben"}, http.StatusCreated)

	/* 1. Die Hintergrundvorgänge. Beide Aufgaben stehen offen, also zählt der
	   Verlauf zwei — unabhängig davon, dass keine von ihnen im Gespräch
	   etwas gesagt hat. */
	verlauf := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	vorgaenge, _ := verlauf["tasks"].([]any)
	if len(vorgaenge) != 2 {
		t.Fatalf("both open tasks belong in the background list: %v", verlauf["tasks"])
	}

	/* 2. Die Suche im Gespräch. Sie findet die Nachricht, nicht das ganze
	   Gespräch — und sie findet sie über den Text wie über den Titel des
	   Vorgangs, zu dem sie wurde. */
	treffer := admin.expect(http.MethodGet, base+"/thread?q=Globex", nil, http.StatusOK)
	eintraege, _ := treffer["entries"].([]any)
	if len(eintraege) == 0 {
		t.Fatal("the search finds nothing at all")
	}
	for _, roh := range eintraege {
		e, _ := roh.(map[string]any)
		text, _ := e["text"].(string)
		titel, _ := e["task_title"].(string)
		if !strings.Contains(text, "Globex") && !strings.Contains(titel, "Globex") {
			t.Fatalf("a hit that carries the term in neither its text nor its task: %v", e)
		}
	}
	if andere := admin.expect(http.MethodGet, base+"/thread?q=Meyer", nil, http.StatusOK); len(andere["entries"].([]any)) == 0 {
		t.Fatal("the second message is not findable")
	}

	/* 3. Ein Prozentzeichen ist kein Platzhalter. Wer nach „%" sucht, sucht
	   das Zeichen — nicht alles. Ohne das Ausmaskieren wäre jede Suche mit
	   einem Prozentzeichen darin die Anzeige des ganzen Verlaufs, und das
	   sähe aus wie ein Treffer. */
	alles := admin.expect(http.MethodGet, base+"/thread?q=%25", nil, http.StatusOK)
	if len(alles["entries"].([]any)) != 0 {
		t.Fatalf("%% matched as a wildcard: %d entries", len(alles["entries"].([]any)))
	}

	/* 4. Eine Suche liefert Fundstellen, nicht das Gespräch: Reaktionen und
	   der Denkzustand bleiben draußen, und die Hintergrundvorgänge auch. */
	if len(treffer["tasks"].([]any)) != 0 {
		t.Error("a list of hits carries no background work")
	}
}

// TestHiringIsADialogueInTheThread walks the whole hiring conversation through
// the team surface's door (#327): a sentence to the People department becomes
// a brief with the frame her playbook counts on, her question comes back into
// the thread, the reply wakes her, the draft she produced stands in the thread
// with the way to its page — and hiring stays a person's click, which the
// thread then shows.
func TestHiringIsADialogueInTheThread(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// The People department, paused so the dispatcher does not run the mock in
	// the middle of the dialogue; her steps are played by hand below.
	people := s.newSupportAgent("people")
	if err := s.registry.SetKilled(ctx, people.ID, true); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/description", map[string]string{"description": "We build bridges."}, http.StatusOK)
	base := "/api/v1/agents/" + people.ID.String()

	// 1. One sentence, said in the thread — and the task carries the brief's
	//    frame, not the bare sentence: the company, the target systems, who
	//    asked. The message itself stays what was said.
	msg := "We need somebody for first-level support in the ticket system."
	created := admin.expect(http.MethodPost, base+"/messages?lang=en", map[string]any{"text": msg}, http.StatusCreated)
	aufgabe, _ := created["task"].(map[string]any)
	body, _ := aufgabe["body"].(string)
	for _, want := range []string{"## Assignment", msg, "## The company", "We build bridges.", "## Frame", "Requested by: admin@test.local", "Available target systems"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the brief frame lacks %q:\n%s", want, body)
		}
	}
	if got := aufgabe["origin"]; got != "chat:admin@test.local" {
		t.Fatalf("origin: %v", got)
	}
	taskID := aufgabe["id"].(string)

	// 2. She asks back — the ordinary blocked edge — and the question is the
	//    next line of the conversation.
	task, err := s.backlog.ClaimNext(ctx, people.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, task.ID, "brief", "sitzung-1", "Should it answer the tickets itself, or only triage and hand them on?"); err != nil {
		t.Fatal(err)
	}
	thread := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	var frage map[string]any
	for _, e := range thread["entries"].([]any) {
		if m := e.(map[string]any); m["kind"] == "question" {
			frage = m
		}
	}
	if frage == nil || frage["task_state"] != "blocked" {
		t.Fatalf("her question has to stand in the thread, waiting: %v", frage)
	}

	// 3. The reply wakes her, and she works on.
	reply := admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/reply",
		map[string]any{"text": "Only triage; the answers stay with people."}, http.StatusOK)
	if reply["woken"] != true {
		t.Fatalf("the reply has to wake her: %v", reply)
	}
	if _, err := s.backlog.ClaimNext(ctx, people.ID); err != nil {
		t.Fatal(err)
	}

	// 4. She drafts. The platform writes the provenance into the recording
	//    (hiring.go, rule 3); the thread reads the draft from there, not from
	//    her report.
	entwurf, err := s.registry.CreateDraft(ctx, s.orgID, "support-1", "Support-1", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	roh, _ := json.Marshal(map[string]string{"status": "agent_drafted", "drafted_agent": entwurf.ID.String(),
		"slug": entwurf.Slug, "display_name": entwurf.DisplayName})
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO recording_events (org_id, agent_id, task_id, kind, payload, created_at)
		 VALUES ($1,$2,$3,'lifecycle',$4,now())`, s.orgID, people.ID, task.ID, roh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateDone,
		"Drafted Support-1 (support-1): first-level triage in the ticket system. Access requested: zammad read.", ""); err != nil {
		t.Fatal(err)
	}
	thread = admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	var ergebnis map[string]any
	for _, e := range thread["entries"].([]any) {
		if m := e.(map[string]any); m["kind"] == "result" {
			ergebnis = m
		}
	}
	if ergebnis == nil {
		t.Fatalf("her report has to stand in the thread: %v", thread["entries"])
	}
	drafts, _ := ergebnis["drafts"].([]any)
	if len(drafts) != 1 {
		t.Fatalf("the draft belongs to the result, got %v", ergebnis["drafts"])
	}
	d := drafts[0].(map[string]any)
	if d["id"] != entwurf.ID.String() || d["display_name"] != "Support-1" {
		t.Fatalf("the draft as the thread shows it: %v", d)
	}
	if _, hired := d["hired_at"]; hired {
		t.Fatalf("a draft has no first day yet: %v", d)
	}

	// 5. Hiring is the person's click, on the agent's page — and the thread
	//    then shows the colleague as hired. There is no hire action for her,
	//    and the thread does not pretend otherwise.
	admin.expect(http.MethodPost, "/api/v1/agents/"+entwurf.ID.String()+"/hire", nil, http.StatusOK)
	thread = admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	for _, e := range thread["entries"].([]any) {
		if m := e.(map[string]any); m["kind"] == "result" {
			d := m["drafts"].([]any)[0].(map[string]any)
			if _, hired := d["hired_at"]; !hired {
				t.Fatalf("after the click the thread has to show the first day: %v", d)
			}
		}
	}

	// And a message to anybody else stays what it was: the sentence, no frame.
	other := s.newSupportAgent("other")
	if err := s.registry.SetKilled(ctx, other.ID, true); err != nil {
		t.Fatal(err)
	}
	plain := admin.expect(http.MethodPost, "/api/v1/agents/"+other.ID.String()+"/messages?lang=en",
		map[string]any{"text": "Please check the Globex invoice."}, http.StatusCreated)
	if b, _ := plain["task"].(map[string]any)["body"].(string); b != "Please check the Globex invoice." {
		t.Fatalf("only the People department gets the frame: %q", b)
	}
}

// teamLogin signs the seeded admin in and turns the team surface on for the
// organisation: it is off by default (#328), and every test in this file
// speaks through it.
func teamLogin(t *testing.T, s *stack) *apiClient {
	t.Helper()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/org/team-surface", map[string]any{"enabled": true}, http.StatusOK)
	return admin
}

// TestTheTeamSurfaceIsAnOptIn checks the switch (#328): off by default, and
// off means the server refuses a message — not only that the interface hides
// the box. /auth/me carries the state, because that is where the interface
// reads which shell to show.
func TestTheTeamSurfaceIsAnOptIn(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("team-optin")
	admin := login(t, s, "admin@test.local", "admin-passwort")
	base := "/api/v1/agents/" + agent.ID.String()

	if me := admin.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["TeamSurface"] != false {
		t.Fatalf("the team surface must be off by default: %v", me["TeamSurface"])
	}
	if got := admin.expect(http.MethodGet, "/api/v1/org/team-surface", nil, http.StatusOK); got["enabled"] != false {
		t.Fatalf("GET /org/team-surface: %v", got)
	}
	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "hallo"}, http.StatusForbidden)
	admin.expect(http.MethodPatch, "/api/v1/org/team-surface", map[string]any{}, http.StatusBadRequest)

	admin.expect(http.MethodPatch, "/api/v1/org/team-surface", map[string]any{"enabled": true}, http.StatusOK)
	if me := admin.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["TeamSurface"] != true {
		t.Fatalf("after switching on, /auth/me must say so: %v", me["TeamSurface"])
	}
	if me := admin.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["Email"] != "admin@test.local" {
		t.Fatalf("/auth/me must still carry the principal: %v", me)
	}
	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "hallo"}, http.StatusCreated)
}
