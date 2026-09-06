package settings

// The switch on the channel back to the project.
//
// It lives beside mail.go for the same reason that one does: a group of keys
// that belong together carries its own defaults, its own validation and its
// own entries in the two maps, instead of scattering them through the general
// list.
//
// **On by default, and off in one move.** That is a decision worth stating
// plainly rather than burying in a table: without it the project knows nothing
// about how covey is actually run — which version installations are on, how
// many agents they hold, whether a release is being adopted at all — and every
// answer to that becomes a guess. What goes out are COUNTS and a version.
// Never a title, never a slug, never a text, never an address, never anything
// an agent produced (internal/telemetry assembles it, and the list stands in
// the documentation as well). `telemetry.mode = off`, or an empty
// `telemetry.url`, or COVEY_TELEMETRY=off in the environment, and nothing
// leaves the machine.

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

const (
	// TelemetryMode: on | off.
	TelemetryMode = "telemetry.mode"
	// TelemetryID is the identity this installation gave itself: a UUID,
	// generated once and kept. It is what makes yesterday's counts and
	// today's the same installation, and it says nothing about who runs it.
	// Written by the installation, not by an administrator — clearing the row
	// makes it a new installation to the other side, which is exactly what
	// somebody who clears it wants.
	TelemetryID = "telemetry.id"
	// TelemetryURL is where it goes. The default is the project's own
	// address; a fork points somewhere else, and an empty value switches the
	// channel off as surely as telemetry.mode = off.
	TelemetryURL = "telemetry.url"
	// TelemetryKey is the optional key of an installation the other side
	// knows by name. Sealed like the SMTP password: it is a credential.
	// #nosec G101 — the key this value is stored under, not the value.
	TelemetryKey = "telemetry.key"
)

// DefaultTelemetryURL is where an installation reports unless told otherwise.
// The path, not the endpoint: the reports and the counts hang below it.
const DefaultTelemetryURL = "https://covey.work/rueckkanal"

func init() {
	Defaults[TelemetryMode] = On
	Defaults[TelemetryID] = ""
	Defaults[TelemetryURL] = DefaultTelemetryURL
	Defaults[TelemetryKey] = ""
	Secrets[TelemetryKey] = true
	ReadOnly[TelemetryID] = true
}

func validateTelemetry(key, value string) error {
	switch key {
	case TelemetryMode:
		if value != On && value != Off {
			return fmt.Errorf("%w: %s must be on or off", ErrInvalid, key)
		}
	case TelemetryURL:
		// Empty is allowed and is the second way to switch the channel off.
		if value == "" {
			return nil
		}
		u, err := url.Parse(value)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: %s must be an absolute http(s) address such as %s",
				ErrInvalid, key, DefaultTelemetryURL)
		}
	}
	return nil
}

// Kennung returns this installation's identity, generating it on first use.
//
// It is written here and not through Set, because telemetry.id is one of the
// keys the installation writes about itself: an administrator who could set it
// by hand could make two installations look like one, and the counts would say
// something that is not true.
func (s *Store) Kennung(ctx context.Context) (string, error) {
	id, err := s.Get(ctx, TelemetryID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(id) != "" {
		return id, nil
	}
	if s == nil || s.pool == nil {
		return "", nil
	}
	neu := uuid.NewString()
	// ON CONFLICT DO NOTHING plus a read back: two processes starting at the
	// same moment must not end up as two installations.
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO system_settings (key, value, updated_at) VALUES ($1,$2,now())
		 ON CONFLICT (key) DO NOTHING`, TelemetryID, neu); err != nil {
		return "", err
	}
	return s.Get(ctx, TelemetryID)
}

// TelemetryOn answers the one question the sender asks: may anything go out at
// all, and where to. An empty address is as good a "no" as the switch.
//
// env is COVEY_TELEMETRY from the environment — "off" there wins over the
// setting, so an operator who cannot reach the interface (or does not want the
// row in the database) has a way that works before the first start.
func (s *Store) TelemetryOn(ctx context.Context, env string) (string, bool) {
	if strings.EqualFold(strings.TrimSpace(env), Off) {
		return "", false
	}
	all, err := s.All(ctx)
	if err != nil {
		return "", false
	}
	if all[TelemetryMode] != On {
		return "", false
	}
	ziel := strings.TrimRight(strings.TrimSpace(all[TelemetryURL]), "/")
	return ziel, ziel != ""
}
