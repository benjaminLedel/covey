package httpapi

// The endpoints of the public website — reachable without a session, because
// whoever asks them has none yet. Today that is one question: does this
// installation accept registrations (feature-requests/002-plattform-registrierung.md).
//
// It is answered by the server rather than baked into the build for the same
// reason the install script is served by the instance: covey is self-hosted,
// and what is true for this installation is not true for the next one. A
// website that guessed would either offer a sign-up nobody can complete, or
// hide one that is open.

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/accounts"
	"covey/internal/buildinfo"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/mail"
	"covey/internal/settings"
	"covey/internal/waitlist"
)

type signupState struct {
	// Mode: off | waitlist | open. off means the website offers nothing.
	Mode string `json:"mode"`
	// SiteName: what this installation calls itself.
	SiteName string `json:"site_name"`
	// Source is the public source of this program (buildinfo.SourceURL). It
	// travels without a session because the AGPL obligation does: whoever is
	// offered the service is owed the source, and the sign-in page is where
	// most people meet the service. Version and commit stay behind the
	// session — which build runs here is nobody else's business; the address
	// of the project is.
	Source string `json:"source"`
}

func (s *Server) handleSignupState(w http.ResponseWriter, r *http.Request) {
	st := signupState{Mode: settings.ModeOff, SiteName: "covey", Source: buildinfo.SourceURL}
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
		// Lang is the language the sign-up page was in — the mail follows it.
		// Empty falls back to English (mail.Lang).
		Lang string `json:"lang"`
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

	// The confirmation decides whether the account is usable, so it is not
	// optional: without a mailer nothing is created here at all. An account
	// whose link could never be sent would be one nobody can report — for
	// reporting it, they would need an account.
	//
	// The mailer's absence is not a bad request and not a permanent state, so
	// it answers 503 with a sentence somebody can act on.
	if s.Mail == nil || !s.Mail.Configured(r.Context()) {
		writeErr(w, http.StatusServiceUnavailable,
			"registration is currently unavailable — this installation cannot send mail")
		return
	}

	// The link's host comes from the CONFIGURED site address where there is
	// one. Deriving it from the request would let anybody who can set a Host
	// header decide which domain a confirmation link points at — and the mail
	// would carry the token there. Behind a proxy that passes the origin
	// through, the fallback is right; on a public instance, COVEY_SITE_URL is
	// what makes it safe (docs/en/operations/mail.md).
	base := s.origin(r)
	lang := mail.Lang(in.Lang)
	site, _ := s.Settings.Get(r.Context(), settings.SiteName)

	acc, err := s.Accounts.Register(r.Context(),
		accounts.Registration{Email: in.Email, DisplayName: in.DisplayName,
			PasswordHash: hash, Verified: false, Lang: lang},
		func(tx pgx.Tx, accountID uuid.UUID) error {
			if mode == settings.ModeWaitlist {
				if _, err := s.Waitlist.Redeem(r.Context(), tx, in.Code, in.Email, accountID); err != nil {
					return err
				}
			}
			token, err := accounts.IssueTokenIn(r.Context(), tx, accountID,
				accounts.PurposeVerify, accounts.VerifyTTL)
			if err != nil {
				return err
			}
			// Sending INSIDE the transaction, deliberately. The alternative is
			// an account plus a burned code plus no mail, which somebody then
			// has to repair by hand; this way a mail server that refuses takes
			// the whole registration back with it. The price is one SMTP round
			// trip inside a short transaction, and the sender has a timeout.
			if err := s.Mail.Send(r.Context(), mail.Message{
				To:      in.Email,
				Subject: mail.Text(lang, "mails.verify.subject", map[string]string{"site": site}),
				Body: mail.Text(lang, "mails.verify.body", map[string]string{
					"site": site, "name": in.DisplayName,
					"link": base + "/verify?token=" + url.QueryEscape(token),
				}),
			}); err != nil {
				return fmt.Errorf("%w: %v", errMailFailed, err)
			}
			return nil
		})
	if err != nil {
		signupError(w, err)
		return
	}
	s.Log.Info("account registered", "email", acc.Email, "mode", mode)
	writeJSON(w, http.StatusCreated, map[string]any{
		"ok": true, "verification_sent": true,
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
// errMailFailed marks the one failure in a registration that is not the
// registrant's fault. It has to be distinguishable, because the answer is a
// different one: not "your input is wrong" but "come back in a minute" — and
// the SMTP error itself stays in the log, where the person who can fix it is
// reading.
var errMailFailed = errors.New("the confirmation mail could not be sent")

func signupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMailFailed):
		writeErr(w, http.StatusServiceUnavailable,
			"registration is currently unavailable — the confirmation mail could not be sent")
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

// --- Confirming an address, and getting back in ---
//
// Both endpoints stay reachable while signup.mode is `off`. A closed instance
// still has accounts that were created while it was open, and the person who
// forgot their password is not a stranger asking to be let in — they are
// already inside.

// handleVerify — POST /api/v1/public/verify: token → confirmed address plus a
// session.
//
// The session is not a convenience. Whoever has just proved that the address
// is theirs would otherwise be sent to a login form to type the password they
// entered two minutes ago — and the first thing they would do on a slow day is
// mistype it.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.signupLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts — please try again later")
		return
	}
	acc, err := s.Accounts.Verify(r.Context(), strings.TrimSpace(in.Token))
	if err != nil {
		if errors.Is(err, accounts.ErrBadToken) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	s.startSession(w, r, acc)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": acc.Email})
}

// handleResendVerification — POST /api/v1/public/verify/resend.
//
// A confirmation link lives 24 hours, and the account it belongs to lives
// longer. Without this endpoint a link that expired in a spam folder would be
// a dead account: registering again answers "address already taken", and the
// way out would run through an administrator with database access.
//
// It answers identically for every address, like the password reset — a known
// unconfirmed account, a confirmed one and an unknown one are one answer.
func (s *Server) handleResendVerification(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil || s.Settings == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Email string `json:"email"`
		Lang  string `json:"lang"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.signupLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts — please try again later")
		return
	}
	if s.Mail == nil || !s.Mail.Configured(r.Context()) {
		writeErr(w, http.StatusServiceUnavailable,
			"this installation cannot send mail — ask an administrator")
		return
	}
	defer writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})

	acc, err := s.Accounts.ByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil || acc.Verified() {
		return // unknown, or nothing left to confirm
	}
	if err := s.Accounts.DropTokens(r.Context(), acc.ID, accounts.PurposeVerify); err != nil {
		s.Log.Warn("could not clear the old confirmation links", "err", err)
		return
	}
	token, err := s.Accounts.IssueToken(r.Context(), acc.ID, accounts.PurposeVerify, accounts.VerifyTTL)
	if err != nil {
		s.Log.Warn("could not issue a confirmation link", "err", err)
		return
	}
	lang := mail.Lang(in.Lang)
	site, _ := s.Settings.Get(r.Context(), settings.SiteName)
	name := acc.DisplayName
	if name == "" {
		name = acc.Email
	}
	if err := s.Mail.Send(r.Context(), mail.Message{
		To:      acc.Email,
		Subject: mail.Text(lang, "mails.verify.subject", map[string]string{"site": site}),
		Body: mail.Text(lang, "mails.verify.body", map[string]string{
			"site": site, "name": name,
			"link": s.origin(r) + "/verify?token=" + url.QueryEscape(token),
		}),
	}); err != nil {
		s.Log.Error("could not send the confirmation mail again", "err", err)
	}
}

// handlePasswordReset — POST /api/v1/public/password-reset: ask for the mail.
//
// It answers the same for a known and an unknown address, and it answers it
// with the same delay class — the work either way is one query. Telling the
// two apart would turn this endpoint into a way of finding out who has an
// account here.
func (s *Server) handlePasswordReset(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil || s.Settings == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Email string `json:"email"`
		Lang  string `json:"lang"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.signupLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts — please try again later")
		return
	}
	if s.Mail == nil || !s.Mail.Configured(r.Context()) {
		writeErr(w, http.StatusServiceUnavailable,
			"this installation cannot send mail — ask an administrator for a new password")
		return
	}

	// From here on the answer is fixed. What follows either happens or does
	// not; the caller learns nothing about which.
	defer writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})

	acc, err := s.Accounts.ByEmail(r.Context(), strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil {
		return // unknown address — and that stays between us and the log
	}
	// Older links die with the new one: two reset mails in the same mailbox,
	// of which the older one still works, is a second chance for whoever got
	// hold of the older mail.
	if err := s.Accounts.DropTokens(r.Context(), acc.ID, accounts.PurposeReset); err != nil {
		s.Log.Warn("could not clear the old reset links", "err", err)
		return
	}
	token, err := s.Accounts.IssueToken(r.Context(), acc.ID, accounts.PurposeReset, accounts.ResetTTL)
	if err != nil {
		s.Log.Warn("could not issue a reset link", "err", err)
		return
	}
	lang := mail.Lang(in.Lang)
	site, _ := s.Settings.Get(r.Context(), settings.SiteName)
	name := acc.DisplayName
	if name == "" {
		name = acc.Email
	}
	if err := s.Mail.Send(r.Context(), mail.Message{
		To:      acc.Email,
		Subject: mail.Text(lang, "mails.reset.subject", map[string]string{"site": site}),
		Body: mail.Text(lang, "mails.reset.body", map[string]string{
			"site": site, "name": name,
			"link": s.origin(r) + "/reset?token=" + url.QueryEscape(token),
		}),
	}); err != nil {
		// Into the log, not into the answer: the person waiting for the mail
		// cannot act on an SMTP error, and whoever can is reading the log.
		s.Log.Error("could not send the reset mail", "err", err)
	}
	_ = s.Accounts.PurgeExpiredTokens(r.Context())
}

// handlePasswordResetConfirm — POST /api/v1/public/password-reset/confirm.
func (s *Server) handlePasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !s.signupLimiter.allow(s.clientIP(r), time.Now()) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts — please try again later")
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
	acc, err := s.Accounts.ResetPassword(r.Context(), strings.TrimSpace(in.Token), hash)
	if err != nil {
		if errors.Is(err, accounts.ErrBadToken) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	s.Log.Info("password reset", "email", acc.Email)
	// No session here, unlike the confirmation: whoever has just set a
	// password should use it once, and a sign-in that fails now is one that
	// would otherwise have failed on the next day, without the reset mail
	// still lying next to it.
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": acc.Email})
}

// startSession signs an account in without a seat. The org gate is what picks
// the membership up from there (FR-002, P5); until then such a session sees
// the instance and no organisation, which is exactly what a freshly confirmed
// account has.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, acc accounts.Account) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		s.Log.Error("could not create a session", "err", err)
		return
	}
	token := hex.EncodeToString(buf)
	if err := s.sessions().Create(r.Context(), hashToken(token), acc.ID, uuid.Nil,
		time.Now().Add(s.SessionTTL)); err != nil {
		s.Log.Error("could not create a session", "err", err)
		return
	}
	s.setSessionCookie(w, token, int(s.SessionTTL.Seconds()))
}
