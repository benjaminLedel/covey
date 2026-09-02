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
	"covey/internal/settings"
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
	// Counted in the text part: the HTML part beside it repeats every line.
	text, _, _ := strings.Cut(msg, "Content-Type: text/html")
	if n := strings.Count(text, "http.post"); n != 3 {
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

	prefsOf := func(m map[string]any) map[string]any { p, _ := m["prefs"].(map[string]any); return p }
	prefs := admin.expect("GET", "/api/v1/auth/notifications", nil, 200)
	if prefsOf(prefs)[notify.ClassDecision] != true {
		t.Errorf("decision = %v, expected true", prefsOf(prefs)[notify.ClassDecision])
	}
	if d, _ := prefs["disabled"].([]any); len(d) != 0 {
		t.Errorf("a fresh instance has switched off %v", d)
	}
	after := admin.expect("PUT", "/api/v1/auth/notifications",
		map[string]bool{notify.ClassDecision: false}, 200)
	if prefsOf(after)[notify.ClassDecision] != false {
		t.Errorf("after switching off: %v", prefsOf(after)[notify.ClassDecision])
	}
	// An unknown class is a typo, not a new feature.
	admin.expect("PUT", "/api/v1/auth/notifications", map[string]bool{"gossip": true}, 400)

	// And it holds beyond the request.
	again := admin.expect("GET", "/api/v1/auth/notifications", nil, 200)
	if prefsOf(again)[notify.ClassDecision] != false {
		t.Error("the switch did not survive the request")
	}

	// What the installation switches off, the answer names (#180) — the page
	// greys the switch out instead of offering one that does nothing.
	if err := s.settings.Set(context.Background(), settings.NotifyClassKey(notify.ClassTask), settings.Off, nil); err != nil {
		t.Fatal(err)
	}
	withOff := admin.expect("GET", "/api/v1/auth/notifications", nil, 200)
	if d, _ := withOff["disabled"].([]any); len(d) != 1 || d[0] != notify.ClassTask {
		t.Errorf("disabled = %v, expected [task]", withOff["disabled"])
	}
}

// The instance's switches (#180): a class switched off for the installation
// is written down for nobody, and the window and the address come from the
// settings rather than from the code or the environment.
func TestInstanceSwitchesGoverntheMails(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	smtp := startFakeSMTP(t)
	s.configureMailer(t, smtp)
	// No pinned window and no SiteURL: everything this store and sender know
	// they know from the settings.
	store := notify.New(s.pool).WithSettings(s.settings)
	sender := &notify.Sender{Pool: s.pool, Mail: mail.New(s.settings), Settings: s.settings,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))}

	agent, err := s.registry.Create(ctx, s.orgID, "regel", "Regel", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	emit := func(class, kind, title string) {
		t.Helper()
		id := uuid.New()
		if err := store.Emit(ctx, notify.Event{
			OrgID: s.orgID, AgentID: agent.ID, Class: class, Kind: kind, SubjectID: &id,
			Title: title, Link: "/costs",
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows := func() int {
		t.Helper()
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM notifications`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	// The master switch: off at the instance, nothing is written — even for
	// a class that is on by default and on for this account.
	if err := s.settings.Set(ctx, settings.NotifyClassKey(notify.ClassCost), settings.Off, nil); err != nil {
		t.Fatal(err)
	}
	emit(notify.ClassCost, notify.KindBudget, "paused at its cap")
	if n := rows(); n != 0 {
		t.Fatalf("%d rows for a class the installation switched off", n)
	}
	if err := s.settings.Set(ctx, settings.NotifyClassKey(notify.ClassCost), settings.On, nil); err != nil {
		t.Fatal(err)
	}

	// The window: with the default the row is not due for five minutes and
	// the sender leaves it; with 0s it goes on the next pass.
	emit(notify.ClassCost, notify.KindBudget, "paused at its cap")
	if sent, err := sender.Once(ctx); err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v — the default window has not passed", sent, err)
	}
	if err := s.settings.Set(ctx, settings.NotifyWindow, "0s", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM notifications`); err != nil {
		t.Fatal(err)
	}
	emit(notify.ClassCost, notify.KindBudget, "paused at its cap")
	if sent, err := sender.Once(ctx); err != nil || sent != 1 {
		t.Fatalf("sent=%d err=%v — a window of 0s sends on the next pass", sent, err)
	}

	// The address: without site.url the mail has no link, with it the link
	// is built from the setting — and the mail carries a styled part beside
	// the text.
	_, _, msg := smtp.snapshot()
	if strings.Contains(msg, "/costs") {
		t.Errorf("a link without a host:\n%s", msg)
	}
	if !strings.Contains(msg, "multipart/alternative") || !strings.Contains(msg, "text/html") {
		t.Errorf("the mail has no HTML part:\n%s", msg)
	}
	if err := s.settings.Set(ctx, settings.SiteURL, "https://covey.example.test/", nil); err != nil {
		t.Fatal(err)
	}
	emit(notify.ClassCost, notify.KindBudget, "paused at its cap")
	if sent, err := sender.Once(ctx); err != nil || sent != 1 {
		t.Fatalf("sent=%d err=%v", sent, err)
	}
	if _, _, msg = smtp.snapshot(); !strings.Contains(msg, "https://covey.example.test/costs") {
		t.Errorf("the link is not built from site.url:\n%s", msg)
	}

	// Switched off while rows were waiting: they are retired, not sent.
	if _, err := s.pool.Exec(ctx, `DELETE FROM notifications`); err != nil {
		t.Fatal(err)
	}
	emit(notify.ClassCost, notify.KindBudget, "paused at its cap")
	if err := s.settings.Set(ctx, settings.NotifyClassKey(notify.ClassCost), settings.Off, nil); err != nil {
		t.Fatal(err)
	}
	if sent, err := sender.Once(ctx); err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v — the class was switched off", sent, err)
	}
	var state string
	if err := s.pool.QueryRow(ctx, `SELECT state FROM notifications LIMIT 1`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "obsolete" {
		t.Errorf("the row stands at %q, expected obsolete", state)
	}
}

// An open point from covey Doctor goes to the organisation's admins alone;
// an approval goes to everybody who may decide it, the owner included (#181).
func TestDoctorsPointsReachOnlyTheOrgAdmin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	store := s.emitter()

	// A second seat: an agent owner with an account of their own.
	ownerAccount, ownerHuman := uuid.New(), uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,'owner@test.local','x','Owner',now())`, ownerAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,'owner@test.local','Owner','x','agent_owner')`, ownerHuman, s.orgID, ownerAccount); err != nil {
		t.Fatal(err)
	}
	agent, err := s.registry.Create(ctx, s.orgID, "doktor", "Doktor", "mock", &ownerHuman)
	if err != nil {
		t.Fatal(err)
	}

	recipients := func(kind string) []string {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `DELETE FROM notifications`); err != nil {
			t.Fatal(err)
		}
		id := uuid.New()
		if err := store.Emit(ctx, notify.Event{
			OrgID: s.orgID, AgentID: agent.ID,
			Class: notify.ClassDecision, Kind: kind, SubjectID: &id,
			Title: "x", Link: "/inbox",
		}); err != nil {
			t.Fatal(err)
		}
		rows, err := s.pool.Query(ctx, `SELECT a.email FROM notifications n JOIN accounts a ON a.id=n.account_id ORDER BY a.email`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var e string
			if err := rows.Scan(&e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		return out
	}

	if got := recipients(notify.KindImprovement); strings.Join(got, ",") != "admin@test.local" {
		t.Errorf("an improvement item reached %v, expected the org admin alone", got)
	}
	if got := recipients(notify.KindApproval); strings.Join(got, ",") != "admin@test.local,owner@test.local" {
		t.Errorf("an approval reached %v, expected admin and owner", got)
	}
}
