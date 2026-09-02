package integration

// The notification mails (#169).
//
// Three properties carry this feature, and each of them is the reason the
// others are worth anything:
//
//   - Ten events become ONE mail. Otherwise the first busy hour teaches
//     everybody to filter these mails into a folder they never open.
//   - What has been dealt with in the meantime produces NO mail. That is what
//     the damping window is really for.
//   - What somebody switched off is never written down at all.

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/accounts"
	"covey/internal/mail"
	"covey/internal/notify"
)

// sender builds a sender over this stack, with the fake SMTP already
// configured.
func (s *stack) sender(t *testing.T, smtp *fakeSMTPServer) *notify.Sender {
	t.Helper()
	s.configureMailer(t, smtp)
	return &notify.Sender{
		Pool: s.pool, Mail: mail.New(s.settings), Settings: s.settings,
		SiteURL: "https://covey.example.test",
		Log:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

// emitter is a notify store whose window has already passed, so a test does
// not have to wait five minutes for its own event.
func (s *stack) emitter() *notify.Store {
	return notify.New(s.pool).WithWindow(-time.Second)
}

func TestManyEventsBecomeOneMail(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t)
	sender := s.sender(t, smtp)
	store := s.emitter()

	agent, err := s.registry.Create(ctx, s.orgID, "melder", "Melder", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}

	// Three approvals, as three separate events — the same shape a guard rail
	// that catches three actions in a row produces.
	for i := 0; i < 3; i++ {
		appr, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "http.post", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		id := appr.ID
		if err := store.Emit(ctx, notify.Event{
			OrgID: s.orgID, AgentID: agent.ID,
			Class: notify.ClassDecision, Kind: notify.KindApproval, SubjectID: &id,
			Title: "Melder is waiting for a release: http.post", Link: "/inbox",
		}); err != nil {
			t.Fatal(err)
		}
	}

	sent, err := sender.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("%d mails went out, expected exactly 1", sent)
	}
	_, rcpts, msg := smtp.snapshot()
	if len(rcpts) != 1 || rcpts[0] != "admin@test.local" {
		t.Fatalf("recipients %v", rcpts)
	}
	if n := strings.Count(msg, "http.post"); n != 3 {
		t.Errorf("the mail carries %d of the 3 events:\n%s", n, msg)
	}
	if !strings.Contains(msg, "https://covey.example.test/inbox") {
		t.Errorf("the mail has no link to act on:\n%s", msg)
	}

	// A second pass has nothing left to do — otherwise a restart would repeat
	// every mail this instance ever sent.
	sent, err = sender.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Errorf("a second pass sent %d mails again", sent)
	}
}

func TestDecidedInTimeMeansNoMail(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t)
	sender := s.sender(t, smtp)
	store := s.emitter()

	agent, err := s.registry.Create(ctx, s.orgID, "schnell", "Schnell", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	appr, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, nil, "http.post", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	id := appr.ID
	if err := store.Emit(ctx, notify.Event{
		OrgID: s.orgID, AgentID: agent.ID,
		Class: notify.ClassDecision, Kind: notify.KindApproval, SubjectID: &id,
		Title: "waiting", Link: "/inbox",
	}); err != nil {
		t.Fatal(err)
	}

	// Somebody was faster than the window.
	if _, err := s.obs.DecideApproval(ctx, s.orgID, appr.ID, true, &s.adminID); err != nil {
		t.Fatal(err)
	}

	sent, err := sender.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Errorf("%d mails about something that was already decided", sent)
	}
	if _, _, msg := smtp.snapshot(); msg != "" {
		t.Errorf("a mail went out anyway:\n%s", msg)
	}
	var state string
	if err := s.pool.QueryRow(ctx, `SELECT state FROM notifications LIMIT 1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "obsolete" {
		t.Errorf("the row stands at %q, expected obsolete — pending would try again forever", state)
	}
}

func TestSwitchedOffIsNeverWrittenDown(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	store := s.emitter()

	acc, err := accounts.New(s.pool).ByEmail(ctx, "admin@test.local")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetPreference(ctx, acc.ID, notify.ClassDecision, false); err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "leise", "Leise", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := store.Emit(ctx, notify.Event{
		OrgID: s.orgID, AgentID: agent.ID,
		Class: notify.ClassDecision, Kind: notify.KindApproval, SubjectID: &id,
		Title: "waiting", Link: "/inbox",
	}); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("%d rows for a class that was switched off", rows)
	}

	// The default for `task` is off as well — the loud class stays quiet until
	// somebody asks for it.
	prefs, err := store.Preferences(ctx, acc.ID)
	if err != nil {
		t.Fatal(err)
	}
	if prefs[notify.ClassTask] {
		t.Error("task notifications are on by default")
	}
	if !prefs[notify.ClassCost] {
		t.Error("cost notifications are off by default")
	}
}

// Without a mailer nothing is attempted and nothing is burned: the rows wait
// for the day somebody configures a mail server.
func TestWithoutAMailerNothingIsLost(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	store := s.emitter()

	agent, err := s.registry.Create(ctx, s.orgID, "geduldig", "Geduldig", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := store.Emit(ctx, notify.Event{
		OrgID: s.orgID, AgentID: agent.ID,
		Class: notify.ClassCost, Kind: notify.KindBudget, SubjectID: &id,
		Title: "paused at its cap", Link: "/costs",
	}); err != nil {
		t.Fatal(err)
	}

	sender := &notify.Sender{Pool: s.pool, Mail: mail.New(s.settings), Settings: s.settings,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))}
	sent, err := sender.Once(ctx)
	if err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v — an unconfigured instance must not try", sent, err)
	}
	var state string
	if err := s.pool.QueryRow(ctx, `SELECT state FROM notifications LIMIT 1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "pending" {
		t.Errorf("the row stands at %q — it has to survive the missing mailer", state)
	}

	// Once there is one, it goes out.
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	sender.SiteURL = "https://covey.example.test"
	sent, err = sender.Once(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Errorf("%d mails after the mailer was configured, expected 1", sent)
	}
}

// The preferences are the person's, and they are answered over the API the
// interface uses.
func TestPreferencesOverTheAPI(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	prefs := admin.expect("GET", "/api/v1/auth/notifications", nil, 200)
	if prefs[notify.ClassDecision] != true {
		t.Errorf("decision = %v, expected true", prefs[notify.ClassDecision])
	}
	after := admin.expect("PUT", "/api/v1/auth/notifications",
		map[string]bool{notify.ClassDecision: false}, 200)
	if after[notify.ClassDecision] != false {
		t.Errorf("after switching off: %v", after[notify.ClassDecision])
	}
	// An unknown class is a typo, not a new feature.
	admin.expect("PUT", "/api/v1/auth/notifications", map[string]bool{"gossip": true}, 400)

	// And it holds beyond the request.
	again := admin.expect("GET", "/api/v1/auth/notifications", nil, 200)
	if again[notify.ClassDecision] != false {
		t.Error("the switch did not survive the request")
	}
}
