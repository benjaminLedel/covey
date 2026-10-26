package engines

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Following a publisher (#662).
//
// A pinned release is a version and a digest somebody copied from the publisher
// into the catalogue. Where the digest is the publisher's own announcement, the
// copy adds no check — only delay, and a break: the artefact sits behind a
// `latest` address, so the day the publisher moves on, the pinned digest no
// longer matches the bytes and a fresh host can install nothing. An entry with
// `follow` asks the publisher instead, at the start of a sandbox, which release
// is current and what its digest is.
//
// What does not change is the check that matters: the bytes are still verified
// against a digest before anything runs, and the digest is still the publisher's.
// What changes is who decides which version that is — the endpoint named here,
// reached with the organisation's own token, instead of a merge in main. That is
// a delegation the entry opts into and spec/26 § Trust records.
//
// The publisher being quiet never stops an agent that started before. The last
// announcement and the last release that actually installed are both kept on
// this host's disk; silence leaves them standing, and an entry's own version is
// the floor for a host that has never heard anything.

// Follow names the endpoint that announces an engine's current release.
type Follow struct {
	// URL answers a GET with `{"version": "1.0.32", "sha256": "<hex>"}` — further
	// fields are ignored. It is asked with the same header and token as the
	// artefact: a publisher that keeps its CLI behind a login keeps the version of
	// it there too.
	URL string `json:"url"`
}

// followBudget bounds the question to the publisher. It sits on the wake path
// like the catalogue fetch, and for the same reason it is short: a publisher
// that has gone quiet must cost a start seconds, not the client's timeout.
const followBudget = 5 * time.Second

// followFresh is how long an announcement is believed without asking again. A
// release is published maybe weekly; an hour of delay is nothing, and a question
// per wake to someone else's server is not something an agent fleet should do.
const followFresh = 10 * time.Minute

// maxAnnouncement caps the answer. The document is a few hundred bytes; anything
// past this is not an announcement.
const maxAnnouncement = 64 << 10

// followed is what this host keeps per followed engine.
type followed struct {
	// URL is the endpoint the record was made for. An entry that follows a
	// different endpoint (the publisher moved, an installation points at a mirror)
	// starts from nothing rather than from another source's word.
	URL string `json:"url"`
	// Announced is the publisher's last answer, Good the last release that
	// installed and verified. They differ while a new release is being fetched,
	// or when it could not be.
	Announced *pin      `json:"announced,omitempty"`
	Good      *pin      `json:"good,omitempty"`
	AskedAt   time.Time `json:"asked_at"`
}

type pin struct {
	Version   string `json:"version"`
	Integrity string `json:"integrity"`
}

func (p *pin) apply(r Release) Release {
	r.Version, r.Integrity = p.Version, p.Integrity
	return r
}

// ensureFollowed installs the release the publisher announces, falling back to
// what this host already has when the announced one cannot be had.
func (s *Store) ensureFollowed(ctx context.Context, r Release, auth Auth, watch func(Progress)) (Layer, error) {
	rec := s.loadFollowed(r)
	if rec.Announced == nil || time.Since(rec.AskedAt) > followFresh {
		s.ask(ctx, r, auth, &rec)
	}
	want := r
	if rec.Announced != nil {
		want = rec.Announced.apply(r)
	}
	l, err := s.ensurePinned(ctx, want, auth, watch)
	if err != nil && rec.Announced != nil {
		// The address is `latest`, so between the announcement and the download
		// the publisher may have moved on: the bytes then match no digest we hold.
		// One fresh question settles that case; a second mismatch is a real one.
		before := *rec.Announced
		if s.ask(ctx, r, auth, &rec) && *rec.Announced != before {
			want = rec.Announced.apply(r)
			l, err = s.ensurePinned(ctx, want, auth, watch)
		}
	}
	if err != nil {
		// The new release could not be installed. The last one that could is still
		// here, verified when it landed; an agent keeps working on it, and the
		// reason goes to the log rather than failing a start that has an engine.
		if rec.Good != nil && rec.Good.Version != want.Version {
			if prev, ok := s.Lookup(r.engine, rec.Good.Version); ok {
				prev.Release = rec.Good.apply(r)
				s.warn("followed engine release could not be installed; the previous one stays",
					r, "wanted", want.Version, "running", rec.Good.Version, "err", err)
				return prev, nil
			}
		}
		return Layer{}, err
	}
	if rec.Good == nil || rec.Good.Version != want.Version || rec.Good.Integrity != want.Integrity {
		rec.Good = &pin{Version: want.Version, Integrity: want.Integrity}
		s.saveFollowed(r, rec)
	}
	return l, nil
}

// ask puts the question to the publisher and records the answer. False means no
// usable answer; the record then keeps what it had, and the reason is logged.
func (s *Store) ask(ctx context.Context, r Release, auth Auth, rec *followed) bool {
	ctx, cancel := context.WithTimeout(ctx, followBudget)
	defer cancel()
	p, err := s.announcement(ctx, r, auth)
	if err != nil {
		s.warn("engine publisher gave no usable announcement; staying on what is known", r, "err", err)
		return false
	}
	rec.URL, rec.Announced, rec.AskedAt = strings.TrimSpace(r.Follow.URL), p, time.Now().UTC()
	s.saveFollowed(r, *rec)
	return true
}

func (s *Store) announcement(ctx context.Context, r Release, auth Auth) (*pin, error) {
	raw := strings.TrimSpace(r.Follow.URL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if header := strings.TrimSpace(r.AuthHeader); header != "" {
		token := strings.TrimSpace(auth.Secret)
		if token == "" && r.AuthEnv != "" {
			token = strings.TrimSpace(os.Getenv(r.AuthEnv))
		}
		if token == "" {
			return nil, fmt.Errorf("%s needs the %s header and no token is at hand", raw, header)
		}
		req.Header.Set(header, authHeaderValue(header, token))
	}
	httpc := s.HTTP
	if httpc == nil {
		httpc = http.DefaultClient
	}
	res, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s is not reachable: %w", raw, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answers %s", raw, res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxAnnouncement+1))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", raw, err)
	}
	if len(body) > maxAnnouncement {
		return nil, fmt.Errorf("%s answers with more than an announcement", raw)
	}
	var doc struct {
		Version string `json:"version"`
		SHA256  string `json:"sha256"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s does not answer with JSON: %w", raw, err)
	}
	// The version becomes a directory name and a mount path, and the digest is
	// the one check the bytes face. Both are read as what they are allowed to be,
	// not believed: a version that safeSegment would have to rewrite is not one.
	v := strings.TrimSpace(doc.Version)
	if v == "" || safeSegment(v) != v {
		return nil, fmt.Errorf("%s announces version %q, which cannot be installed", raw, doc.Version)
	}
	sum := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(doc.SHA256), "sha256:"))
	if b, err := hex.DecodeString(sum); err != nil || len(b) != 32 {
		return nil, fmt.Errorf("%s announces %q as the sha256 of %s, which is not one", raw, doc.SHA256, v)
	}
	return &pin{Version: v, Integrity: "sha256:" + sum}, nil
}

func (s *Store) followedPath(r Release) string {
	return filepath.Join(s.Dir, safeSegment(r.engine), "followed.json")
}

// loadFollowed reads this host's record. Absent, unreadable or made for another
// endpoint is the same as none: the entry's own version is then the floor.
func (s *Store) loadFollowed(r Release) followed {
	body, err := os.ReadFile(s.followedPath(r))
	if err != nil {
		return followed{}
	}
	var rec followed
	if json.Unmarshal(body, &rec) != nil || rec.URL != strings.TrimSpace(r.Follow.URL) {
		return followed{}
	}
	return rec
}

// saveFollowed keeps the record. A failure costs a question to the publisher at
// the next start and nothing else, so it is logged and not returned.
func (s *Store) saveFollowed(r Release, rec followed) {
	rec.URL = strings.TrimSpace(r.Follow.URL)
	err := func() error {
		path := s.followedPath(r)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		body, err := json.MarshalIndent(rec, "", "  ")
		if err != nil {
			return err
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}()
	if err != nil {
		s.warn("engine follow record could not be written", r, "err", err)
	}
}

func (s *Store) warn(msg string, r Release, args ...any) {
	if s.Log != nil {
		s.Log.Warn(msg, append([]any{"engine", r.engine, "follow", r.Follow.URL}, args...)...)
	}
}

var errFollowKind = errors.New("only a fetched artefact (tarball or file) can follow its publisher: the announced digest is checked against bytes this process downloads")
