// Package telemetry is the channel back to the project: once a day a handful
// of counts, and — where an installation has no tracker account of its own —
// the platform findings its covey Doctor wrote.
//
// **What goes out, in full:** the version this build is, how many
// organisations, humans and agents exist, how many tasks ran in the last day
// and how they ended, which runtimes and which target systems are in use, and
// whether a runner is connected. Numbers and names of things that are part of
// covey.
//
// **What never goes out:** a task title, an agent slug, a prompt, a result, a
// mail address, a repository, a URL, a customer name, an organisation name.
// Nothing an agent produced and nothing anybody typed. The assembly is in one
// function below, and it is short on purpose — this is the list, and a reader
// should be able to check it in a minute.
//
// It is ON by default (settings.TelemetryMode), because an installation that
// has to be asked to report reports nothing, and then nobody knows which
// version is actually being run when a bug arrives. It goes off with one
// setting, one empty address, or COVEY_TELEMETRY=off — and off means nothing
// leaves the machine, not "a little less".
//
// The identity is a UUID this installation gave itself. It ties yesterday's
// counts to today's and says nothing about who runs it.
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/buildinfo"
	"covey/internal/settings"
)

// Zeitplan: the first send waits a little (an installation that is starting
// has better things to do), then it is once a day. A day is the resolution of
// the whole thing — the other side keeps one row per installation and day.
const (
	ersteVerzoegerung = 5 * time.Minute
	abstand           = 24 * time.Hour
	zeitlimit         = 20 * time.Second
)

type Sender struct {
	Pool     *pgxpool.Pool
	Settings *settings.Store
	Log      *slog.Logger
	// Env is COVEY_TELEMETRY from the process environment: "off" there wins
	// over the setting.
	Env string
	// Client is the way out. Nil = the default with a timeout.
	Client *http.Client
}

// Loop sends the counts once a day for as long as the process runs.
func (s *Sender) Loop(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(ersteVerzoegerung):
	}
	t := time.NewTicker(abstand)
	defer t.Stop()
	s.Send(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Send(ctx)
		}
	}
}

// Send collects the counts and sends them. Exported so `covey doctor` and a
// test can do it without waiting a day.
func (s *Sender) Send(ctx context.Context) {
	ziel, an := s.Settings.TelemetryOn(ctx, s.Env)
	if !an {
		return
	}
	zahlen, err := s.Zahlen(ctx)
	if err != nil {
		s.Log.Debug("telemetry: counts not collected", "err", err)
		return
	}
	if err := s.post(ctx, ziel+"/telemetrie", map[string]any{"zahlen": zahlen}); err != nil {
		// Debug and not Warn: an installation without a way out to the
		// internet is a normal state, and a daily warning about it would be
		// noise an operator learns to ignore.
		s.Log.Debug("telemetry: not sent", "err", err)
	}
}

// Bericht sends one platform finding upstream — the way for an installation
// that has no account on the forge of its own. Reports arrive there in a queue
// a person releases; nothing reaches a tracker unread.
//
// It reports what happened to the caller, because the caller is an agent
// action that has to tell its agent whether the finding is on its way or
// whether it has to stay in the review.
func (s *Sender) Bericht(ctx context.Context, agent, titel, text string) (string, error) {
	ziel, an := s.Settings.TelemetryOn(ctx, s.Env)
	if !an {
		return "", fmt.Errorf("the channel to the project is switched off on this installation")
	}
	var antwort struct {
		Status   string `json:"status"`
		IssueURL string `json:"issue_url"`
		Hinweis  string `json:"hinweis"`
		Fehler   string `json:"fehler"`
	}
	err := s.postMitAntwort(ctx, ziel+"/bericht", map[string]any{
		"agent": agent, "titel": titel, "text": text,
	}, &antwort)
	if err != nil {
		return "", err
	}
	if antwort.IssueURL != "" {
		return antwort.IssueURL, nil
	}
	// No address yet is the normal case: it is waiting for a person.
	return "", nil
}

// Zahlen is the whole list of what leaves this installation. Every value is a
// number or the name of something that is part of covey; nothing here comes
// from a prompt, a task or a target system's content.
func (s *Sender) Zahlen(ctx context.Context) (map[string]any, error) {
	if s.Pool == nil {
		return nil, fmt.Errorf("no database")
	}
	out := map[string]any{}
	zaehl := func(name, query string) error {
		var n int
		if err := s.Pool.QueryRow(ctx, query).Scan(&n); err != nil {
			return err
		}
		out[name] = n
		return nil
	}
	for name, query := range map[string]string{
		"orgs":             `SELECT count(*) FROM organizations`,
		"humans":           `SELECT count(*) FROM humans`,
		"agents":           `SELECT count(*) FROM agents`,
		"agents_hired":     `SELECT count(*) FROM agents WHERE hired_at IS NOT NULL`,
		"tasks_24h":        `SELECT count(*) FROM backlog_tasks WHERE created_at > now() - interval '1 day'`,
		"tasks_done_24h":   `SELECT count(*) FROM backlog_tasks WHERE state='done' AND updated_at > now() - interval '1 day'`,
		"tasks_failed_24h": `SELECT count(*) FROM backlog_tasks WHERE state='failed' AND updated_at > now() - interval '1 day'`,
		"tasks_blocked":    `SELECT count(*) FROM backlog_tasks WHERE state='blocked'`,
		"runners":          `SELECT count(*) FROM runners`,
	} {
		if err := zaehl(name, query); err != nil {
			return nil, err
		}
	}
	// Runtimes and target systems by NAME and count — which engines and which
	// plugins are in use is a fact about covey. Which project, which server,
	// which mailbox is not, and none of that is read here.
	if m, err := s.gruppiert(ctx, `SELECT runtime, count(*) FROM agents GROUP BY runtime`); err == nil {
		out["runtimes"] = m
	}
	if m, err := s.gruppiert(ctx, `SELECT name, count(*) FROM target_plugins WHERE enabled GROUP BY name`); err == nil {
		out["targets"] = m
	}
	b := buildinfo.Get()
	out["version"] = b.Version
	out["commit"] = b.Commit
	return out, nil
}

func (s *Sender) gruppiert(ctx context.Context, query string) (map[string]int, error) {
	rows, err := s.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var n int
		if err := rows.Scan(&name, &n); err != nil {
			return nil, err
		}
		if strings.TrimSpace(name) != "" {
			out[name] = n
		}
	}
	return out, rows.Err()
}

func (s *Sender) post(ctx context.Context, url string, koerper map[string]any) error {
	return s.postMitAntwort(ctx, url, koerper, nil)
}

// postMitAntwort adds what every request carries: who is sending, which
// version, and the key where there is one.
func (s *Sender) postMitAntwort(ctx context.Context, url string, koerper map[string]any, ziel any) error {
	kennung, err := s.Settings.Kennung(ctx)
	if err != nil || kennung == "" {
		return fmt.Errorf("no identity for this installation: %v", err)
	}
	koerper["kennung"] = kennung
	koerper["version"] = buildinfo.Get().Version
	if key, err := s.Settings.GetSecret(ctx, settings.TelemetryKey); err == nil && key != "" {
		koerper["schluessel"] = key
	}
	roh, err := json.Marshal(koerper)
	if err != nil {
		return err
	}
	ctx, ab := context.WithTimeout(ctx, zeitlimit)
	defer ab()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(roh))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "covey/"+buildinfo.Get().Version)

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: zeitlimit}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	antwort, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The other side's own words: "we already have this title" is more
		// useful to whoever reads it than a translated version of it.
		var mit struct {
			Fehler string `json:"fehler"`
		}
		_ = json.Unmarshal(antwort, &mit)
		if mit.Fehler != "" {
			return fmt.Errorf("%s", mit.Fehler)
		}
		return fmt.Errorf("the project's endpoint answered %d", resp.StatusCode)
	}
	if ziel != nil {
		return json.Unmarshal(antwort, ziel)
	}
	return nil
}
