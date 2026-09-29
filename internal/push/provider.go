package push

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strconv"
	"sync"

	"covey/internal/settings"
)

// Source is where a round of the notifier gets its sender. Nil with no error
// means push is off.
type Source interface {
	Sender(ctx context.Context) (Sender, error)
}

// Provider answers the senders the instance settings call for (#431): its
// own notifications go directly through FCM, through the relay, or nowhere,
// and in direct mode it may deliver for others.
//
// It reads the settings on every call — a change takes effect on the next
// round, without a restart — and builds a sender anew only when what it is
// built from changed. The FCM sender, with the access token it holds, lives
// as long as the service account does: switching the relay for others on and
// off does not cost a token exchange.
type Provider struct {
	Settings *settings.Store
	Env      settings.PushEnv
	Log      *slog.Logger
	// FCMEndpoint replaces Google's address in tests.
	FCMEndpoint string

	mu        sync.Mutex
	seen      string
	credsHash string
	fcm       *FCM
	own       Sender
	others    Sender
}

// Config is the effective configuration, environment defaults included.
func (p *Provider) Config(ctx context.Context) (settings.Push, error) {
	return p.Settings.Push(ctx, p.Env)
}

// Sender is the sender for this instance's own notifications.
func (p *Provider) Sender(ctx context.Context) (Sender, error) {
	own, _, err := p.current(ctx)
	return own, err
}

// Relaying is the sender for what other instances hand over; nil unless this
// instance sends directly and was told to relay.
func (p *Provider) Relaying(ctx context.Context) (Sender, error) {
	_, others, err := p.current(ctx)
	return others, err
}

func (p *Provider) current(ctx context.Context) (Sender, Sender, error) {
	cfg, err := p.Config(ctx)
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.Sum256([]byte(cfg.Credentials))
	creds := hex.EncodeToString(sum[:])
	key := cfg.Mode + "\x00" + cfg.RelayURL + "\x00" + strconv.FormatBool(cfg.RelayAccept) + "\x00" + creds

	p.mu.Lock()
	defer p.mu.Unlock()
	if key == p.seen {
		return p.own, p.others, nil
	}
	if creds != p.credsHash {
		p.credsHash, p.fcm = creds, nil
		if cfg.Credentials != "" {
			// The store checks an account before sealing it; one from the
			// environment is checked at startup. What fails here anyway is
			// said once, below, and not on every round.
			if f, err := ParseFCM(cfg.Credentials); err == nil {
				if p.FCMEndpoint != "" {
					f.Endpoint = p.FCMEndpoint
				}
				p.fcm = f
			} else if p.Log != nil {
				p.Log.Warn("push: the service account is unusable", "err", err)
			}
		}
	}
	p.own, p.others = nil, nil
	switch cfg.Mode {
	case settings.PushDirect:
		if p.fcm != nil {
			p.own = p.fcm
			if cfg.RelayAccept {
				p.others = p.fcm
			}
		}
	case settings.PushRelay:
		p.own = NewRelay(cfg.RelayURL)
	}
	p.seen = key
	if p.Log != nil {
		switch {
		case cfg.Mode == settings.PushDirect && p.fcm == nil:
			p.Log.Warn("push: direct, but no usable service account — nothing is sent")
		case cfg.Mode == settings.PushDirect:
			p.Log.Info("push: direct through FCM", "project", p.fcm.projectID,
				"credentials", cfg.CredentialsFrom, "relay_for_others", cfg.RelayAccept)
		case cfg.Mode == settings.PushRelay:
			p.Log.Info("push: through the relay", "relay", cfg.RelayURL)
		default:
			p.Log.Info("push: off")
		}
	}
	return p.own, p.others, nil
}
