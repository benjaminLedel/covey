package httpapi

// The endpoints of the public website — reachable without a session, because
// whoever asks them has none yet. Today that is one question: does this
// installation accept registrations (feature-requests/002-plattform-registrierung.md).
//
// It is answered by the server rather than baked into the build for the same
// reason the install script is served by the instance: Covey is self-hosted,
// and what is true for this installation is not true for the next one. A
// website that guessed would either offer a sign-up nobody can complete, or
// hide one that is open.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/accounts"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/settings"
	"covey/internal/waitlist"
)

type signupState struct {
	// Mode: off | waitlist | open. off means the website offers nothing.
	Mode string `json:"mode"`
	// SiteName: what this installation calls itself.
	SiteName string `json:"site_name"`
}

func (s *Server) handleSignupState(w http.ResponseWriter, r *http.Request) {
	st := signupState{Mode: settings.ModeOff, SiteName: "Covey"}
	if s.Settings != nil {
		st.Mode = s.Settings.Mode(r.Context())
		if name, err := s.Settings.Get(r.Context(), settings.SiteName); err == nil && name != "" {
			st.SiteName = name
		}
	}
	// Not cached: whoever closes the registration wants it closed now, not
	// after a proxy's TTL has run out.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

const minSignupPassword = 8

// handleSignup creates an account against a waitlist code.
//
// Deliberately no organisation is chosen here. The account comes into being
// first; whether its holder joins one or founds their own is decided after the
// confirmation, in the org gate (FR-002). That is what makes an account able
// to belong to several organisations later on.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	// A closed instance answers 404 and not 403: whether it COULD be opened is
	// nobody's business who is not administering it.
	if s.Settings == nil || s.Accounts == nil || s.Waitlist == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	mode := s.Settings.Mode(r.Context())
	if mode == settings.ModeOff {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}

	var in struct {
		Code        string `json:"code"`
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Password    string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	in.DisplayName = strings.TrimSpace(in.DisplayName)

	// The rate limit sits before every check that touches the database, and it
	// counts the attempt rather than the failure: guessing codes is the point
	// of the exercise here, and every guess is one request.
	if !s.signupLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts — please try again later")
		return
	}

	if !strings.Contains(in.Email, "@") || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "name and e-mail address are required")
		return
	}
	if len(in.Password) < minSignupPassword {
		writeErr(w, http.StatusBadRequest, "password needs at least 8 characters")
		return
	}
	hash, err := identbuiltin.HashPassword(in.Password)
	if err != nil {
		mapErr(w, err)
		return
	}

	// Without a mailer there is nobody to confirm the address, so the account
	// counts as confirmed. As soon as the mailer exists (P3) this turns into
	// sending the verification link.
	verificationSent := false

	acc, err := s.Accounts.Register(r.Context(),
		accounts.Registration{Email: in.Email, DisplayName: in.DisplayName,
			PasswordHash: hash, Verified: !verificationSent},
		func(tx pgx.Tx, accountID uuid.UUID) error {
			if mode != settings.ModeWaitlist {
				return nil // open registration: no code
			}
			_, err := s.Waitlist.Redeem(r.Context(), tx, in.Code, in.Email, accountID)
			return err
		})
	if err != nil {
		signupError(w, err)
		return
	}
	s.Log.Info("account registered", "email", acc.Email, "mode", mode)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "verification_sent": verificationSent,
	})
}

// signupError keeps the reasons apart instead of levelling them into "did not
// work". Whoever holds a code learns only about their own, and "used up"
// versus "expired" sends them down different paths — one asks for a new code,
// the other asks whether they are too late.
//
// That an address is already taken is said out loud too. This is a sign-up,
// not a password reset: the enumeration argument applies where an attacker
// learns something about somebody ELSE's account, whereas here somebody who is
// told nothing simply waits for a mail that will never arrive.
func signupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, waitlist.ErrMalformed), errors.Is(err, waitlist.ErrUnknown):
		writeErr(w, http.StatusBadRequest, "this code is unknown")
	case errors.Is(err, waitlist.ErrRevoked):
		writeErr(w, http.StatusBadRequest, "this code has been revoked")
	case errors.Is(err, waitlist.ErrExpired):
		writeErr(w, http.StatusBadRequest, "this code has expired")
	case errors.Is(err, waitlist.ErrUsedUp):
		writeErr(w, http.StatusBadRequest, "this code has already been used up")
	case errors.Is(err, waitlist.ErrEmailMismatch):
		writeErr(w, http.StatusBadRequest, "this code applies to a different e-mail address")
	case errors.Is(err, accounts.ErrEmailTaken):
		writeErr(w, http.StatusConflict, "an account already exists for this e-mail address")
	default:
		mapErr(w, err)
	}
}
