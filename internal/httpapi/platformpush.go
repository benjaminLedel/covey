package httpapi

// How the installation sends push notifications (#431), seen from the
// platform page: the mode, the relay, the service account, and a test.
//
// The keys are ordinary settings (internal/settings/push.go) and could be
// set through /platform/settings as well; this route exists because the
// page needs what the generic list cannot give — the value the environment
// supplies where nothing is set, and which project the sealed account
// belongs to.

import (
	"context"
	"errors"
	"net/http"
	"time"

	"covey/internal/push"
	"covey/internal/settings"
)

// testPushTimeout bounds the token exchange the test does.
const testPushTimeout = 20 * time.Second

type pushCredentialsView struct {
	Set bool `json:"set"`
	// Source: settings | environment. An account from the environment
	// cannot be removed here, only replaced.
	Source      string `json:"source,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	ClientEmail string `json:"client_email,omitempty"`
	// Error: the account is there but unusable — possible only for one from
	// the environment that changed since the start, or a sealed one whose
	// key changed.
	Error string `json:"error,omitempty"`
}

type platformPushView struct {
	Mode          string              `json:"mode"`
	RelayURL      string              `json:"relay_url"`
	RelayAccept   bool                `json:"relay_accept"`
	Credentials   pushCredentialsView `json:"credentials"`
	LastTestAt    string              `json:"last_test_at"`
	LastTestError string              `json:"last_test_error"`
}

func (s *Server) platformPush(ctx context.Context) (platformPushView, error) {
	cfg, err := s.Push.Config(ctx)
	if err != nil {
		return platformPushView{}, err
	}
	v := platformPushView{
		Mode: cfg.Mode, RelayURL: cfg.RelayURL, RelayAccept: cfg.RelayAccept,
		LastTestAt: cfg.LastTestAt, LastTestError: cfg.LastTestError,
	}
	if cfg.Credentials != "" {
		v.Credentials = pushCredentialsView{Set: true, Source: cfg.CredentialsFrom}
		if sa, err := settings.ParseServiceAccount(cfg.Credentials); err != nil {
			v.Credentials.Error = err.Error()
		} else {
			// Neither is secret — the account's address is on every token
			// it asks for — and both say which project is configured.
			v.Credentials.ProjectID, v.Credentials.ClientEmail = sa.ProjectID, sa.ClientEmail
		}
	}
	return v, nil
}

func (s *Server) writePlatformPush(w http.ResponseWriter, r *http.Request) {
	v, err := s.platformPush(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleGetPlatformPush — GET /api/v1/platform/push.
func (s *Server) handleGetPlatformPush(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no push notifications")
		return
	}
	s.writePlatformPush(w, r)
}

// settingErr answers a store's refusal the way handleSetSetting does.
func settingErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, settings.ErrUnknownKey):
		writeErr(w, http.StatusNotFound, err.Error())
	case errors.Is(err, settings.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	default:
		mapErr(w, err)
	}
}

// handleSetPlatformPush — PATCH /api/v1/platform/push: mode, relay_url,
// relay_accept, each optional. An empty mode or relay_url hands the key back
// to the environment. It applies on the notifier's next round.
func (s *Server) handleSetPlatformPush(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no push notifications")
		return
	}
	var in struct {
		Mode        *string `json:"mode"`
		RelayURL    *string `json:"relay_url"`
		RelayAccept *bool   `json:"relay_accept"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p := principalFrom(r)
	by := &p.AccountID
	set := map[string]*string{settings.PushMode: in.Mode, settings.PushRelayURL: in.RelayURL}
	if in.RelayAccept != nil {
		v := settings.Off
		if *in.RelayAccept {
			v = settings.On
		}
		set[settings.PushRelayAccept] = &v
	}
	// Checked all before any is stored: a refused relay address must not
	// leave the mode switched to it.
	for key, v := range set {
		if v == nil {
			continue
		}
		if err := s.Settings.Check(key, *v); err != nil {
			settingErr(w, err)
			return
		}
	}
	for key, v := range set {
		if v == nil {
			continue
		}
		if err := s.Settings.Set(r.Context(), key, *v, by); err != nil {
			settingErr(w, err)
			return
		}
	}
	s.writePlatformPush(w, r)
}

// handleSetPushCredentials — PUT /api/v1/platform/push/credentials with
// {"credentials": "<the service account JSON>"}. The page reads the file in
// the browser and sends its text; the store checks it and seals it.
func (s *Server) handleSetPushCredentials(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no push notifications")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var in struct {
		Credentials string `json:"credentials"`
	}
	if err := readJSON(r, &in); err != nil || in.Credentials == "" {
		writeErr(w, http.StatusBadRequest, "expected {\"credentials\": \"<service account JSON>\"}")
		return
	}
	p := principalFrom(r)
	if err := s.Settings.SetSecret(r.Context(), settings.PushCredentials, in.Credentials, &p.AccountID); err != nil {
		settingErr(w, err)
		return
	}
	s.writePlatformPush(w, r)
}

// handleDeletePushCredentials — DELETE /api/v1/platform/push/credentials:
// the stored account goes; one from the environment applies again.
func (s *Server) handleDeletePushCredentials(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no push notifications")
		return
	}
	p := principalFrom(r)
	if err := s.Settings.SetSecret(r.Context(), settings.PushCredentials, "", &p.AccountID); err != nil {
		settingErr(w, err)
		return
	}
	s.writePlatformPush(w, r)
}

// handleTestPush — POST /api/v1/platform/push/test: the account, as stored,
// asks Google for an access token. That proves the account and needs no
// device; whether the Firebase project holds the app's APNs key only Apple
// answers, on the first notification to an iPhone. Recorded either way, as
// the test mail is.
func (s *Server) handleTestPush(w http.ResponseWriter, r *http.Request) {
	if s.Push == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no push notifications")
		return
	}
	cfg, err := s.Push.Config(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if cfg.Credentials == "" {
		writeErr(w, http.StatusBadRequest, "no service account is configured")
		return
	}
	var testErr error
	f, err := push.ParseFCM(cfg.Credentials)
	if err != nil {
		testErr = err
	} else {
		if s.Push.FCMEndpoint != "" {
			f.Endpoint = s.Push.FCMEndpoint
		}
		ctx, cancel := context.WithTimeout(r.Context(), testPushTimeout)
		testErr = f.Check(ctx)
		cancel()
	}
	msg := ""
	if testErr != nil {
		msg = testErr.Error()
	}
	if err := s.Settings.RecordPushTest(r.Context(), time.Now(), msg); err != nil {
		s.Log.Warn("could not record the push test's result", "err", err)
	}
	if testErr != nil {
		// Verbatim, as the mail test's: Google's "invalid_grant" sends
		// somebody to a deleted key, a timeout to the egress.
		writeErr(w, http.StatusBadGateway, msg)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project_id": f.ProjectID()})
}
