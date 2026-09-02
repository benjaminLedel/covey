package httpapi

// The instance's mailer, seen from the outside: one button.
//
// The test mail is the whole reason the mail configuration belongs in the
// product and not in an environment variable. A wrong SMTP setting is
// otherwise discovered by the first person whose verification link never
// arrives — and that person cannot report it, because reporting it would
// require an account.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"covey/internal/mail"
	"covey/internal/settings"
)

// testMailTimeout bounds the attempt. A mail server that accepts the
// connection and then says nothing must not hold the admin's browser open
// indefinitely; 30 seconds is long enough for a slow greylisting relay and
// short enough to still be an answer.
const testMailTimeout = 30 * time.Second

// handleTestMail — POST /api/v1/platform/mail/test.
//
// It sends to the signed-in administrator, over the SAME path as a real mail:
// the same sender, the same settings read, only the body differs. A test that
// travels a different route can pass while the real mail fails, and then it
// has certified exactly nothing.
//
// It sends the settings AS STORED, not the values in an unsaved form — save
// first, then test, so that what was proven is what will run.
func (s *Server) handleTestMail(w http.ResponseWriter, r *http.Request) {
	if s.Mail == nil || s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation has no mailer")
		return
	}
	p := principalFrom(r)
	if p.Email == "" {
		writeErr(w, http.StatusBadRequest, "your account has no e-mail address to send to")
		return
	}

	cfg, err := s.Settings.Mail(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	site, _ := s.Settings.Get(r.Context(), settings.SiteName)

	ctx, cancel := context.WithTimeout(r.Context(), testMailTimeout)
	defer cancel()
	sendErr := s.Mail.Send(ctx, mail.Message{
		To:      p.Email,
		Subject: fmt.Sprintf("%s: test mail", site),
		Body: fmt.Sprintf(`This is the test mail from %s.

If you are reading it, the installation can send mail: %s over %s as %s.

Nothing follows from this mail. It was triggered by hand on the platform's
mail settings, and its only purpose is to prove that the path works before
somebody's verification link depends on it.
`, site, cfg.Addr(), cfg.Security, cfg.Sender()),
	})

	// Recorded either way, and deliberately not inside the send: what is worth
	// keeping is that somebody tried and what came back — a failed attempt is
	// exactly the state the registration gate has to see.
	msg := ""
	if sendErr != nil {
		msg = sendErr.Error()
	}
	if err := s.Settings.RecordMailTest(r.Context(), time.Now(), msg); err != nil {
		s.Log.Warn("could not record the test mail's result", "err", err)
	}

	if sendErr != nil {
		status := http.StatusBadGateway
		if errors.Is(sendErr, mail.ErrNotConfigured) {
			status = http.StatusBadRequest
		}
		// Verbatim. An SMTP server's rejection says what is wrong far more
		// precisely than any sentence we could put in its place — "535 5.7.8
		// authentication failed" sends somebody to the password, "connection
		// refused" to the port.
		writeErr(w, status, sendErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "to": p.Email})
}
