package teamsapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/google/uuid"
)

// Signing the writer in, in the chat (#713).
//
// First choice is Teams' single sign-on for bots: an OAuth card naming the
// app's resource; Teams answers with a signin/tokenExchange invoke carrying
// a token for exactly the person in front of it. When that cannot work — no
// consent yet, single sign-on not set up in the tenant — the card's button
// is a covey link that signs the person in through the browser. That link
// names the person who wrote, and it ends only for that same Microsoft user.

// AskSignIn sends the sign-in card into the conversation.
func (s *Service) AskSignIn(ctx context.Context, a teams.Activity, siteURL, link string) error {
	c, err := s.connector()
	if err != nil {
		return err
	}
	card := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.oauth",
			"content": map[string]any{
				"text":           "This colleague works with your own Teams access. Sign in once, then your message is handled.",
				"connectionName": s.SSOConnection(),
				"tokenExchangeResource": map[string]any{
					"id":  uuid.NewString(),
					"uri": s.SSOResource(siteURL),
				},
				"buttons": []map[string]any{{"type": "signin", "title": "Sign in", "value": link}},
			},
		}},
	}
	_, err = c.SendActivity(ctx, a.ServiceURL, a.Conversation.ID, a.ID, card)
	return err
}

// OfferLink posts the browser sign-in as a plain card, for when the
// exchange failed.
func (s *Service) OfferLink(ctx context.Context, a teams.Activity, link string) error {
	c, err := s.connector()
	if err != nil {
		return err
	}
	act := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"content": map[string]any{
				"type": "AdaptiveCard", "version": "1.4",
				"body": []map[string]any{{"type": "TextBlock", "wrap": true,
					"text": "Signing in silently did not work. Sign in once in the browser; your message is handled afterwards."}},
				"actions": []map[string]any{{"type": "Action.OpenUrl", "title": "Sign in", "url": link}},
			},
		}},
	}
	_, err = c.SendActivity(ctx, a.ServiceURL, a.Conversation.ID, "", act)
	return err
}

const personLinkTTL = time.Hour

// personMAC signs a sign-in link with the app's client secret as the HMAC
// key.
func (s *Service) personMAC(raw []byte) string {
	m := hmac.New(sha256.New, []byte(s.cfg.AppPassword))
	m.Write([]byte("covey teamsapp person link\x00"))
	m.Write(raw)
	return hex.EncodeToString(m.Sum(nil))
}

// PersonLinkToken signs (tenant, person) for the browser sign-in link.
func (s *Service) PersonLinkToken(tenant, oid string) string {
	payload := tenant + "|" + oid + "|" + strconv.FormatInt(time.Now().Add(personLinkTTL).Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.personMAC([]byte(payload))
}

// ErrPersonLink: the sign-in link is forged or expired.
var ErrPersonLink = errors.New("teamsapp: this sign-in link is invalid or expired — write to the bot again for a new one")

// CheckPersonLink returns the tenant and person a link was made for.
func (s *Service) CheckPersonLink(tok string) (tenant, oid string, err error) {
	enc, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return "", "", ErrPersonLink
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", "", ErrPersonLink
	}
	if !hmac.Equal([]byte(sig), []byte(s.personMAC(raw))) {
		return "", "", ErrPersonLink
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return "", "", ErrPersonLink
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", "", ErrPersonLink
	}
	return parts[0], parts[1], nil
}
