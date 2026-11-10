package teamsuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/msauth"
	"covey/internal/teamsapp"
)

// The agent works as the person who wrote (#713). Single sign-on in the chat
// with the organisation's bot gives covey a token for exactly the person
// writing; it is kept per bot and Microsoft user, and a task from that
// person's message uses it and nothing else.

// ErrNoPerson: the person a task works for has not signed in (or no longer
// refreshes).
var ErrNoPerson = errors.New("teamsuser: the person this task works for has not signed in to covey in Teams yet")

func personAAD(bot uuid.UUID, oid string) []byte {
	return []byte("teamsuser-person:" + bot.String() + ":" + oid)
}

func personKey(bot uuid.UUID, oid string) string { return bot.String() + "|" + oid }

// HasPerson answers whether covey holds a token for this Microsoft user.
func (s *Service) HasPerson(ctx context.Context, bot uuid.UUID, oid string) bool {
	if s == nil || oid == "" {
		return false
	}
	var ok bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teamsuser_people WHERE bot_id = $1 AND oid = $2)`, bot, oid).Scan(&ok)
	return ok
}

// Exchange trades the token Teams handed the bot for the writer of a message
// (signin/tokenExchange) for a delegated Graph token, checks it is the same
// person (Graph's /me against the activity's aadObjectId), and stores it.
func (s *Service) Exchange(ctx context.Context, ref teamsapp.DelegationRef, oid, ssoToken string) error {
	tok, err := ref.App.OnBehalfOf(ctx, ref.Tenant, ssoToken, Scope)
	if err != nil {
		return err
	}
	return s.keepPerson(ctx, ref.BotID, oid, tok)
}

// StartPersonSignIn is the browser sign-in the bot offers when single
// sign-on did not work.
func (s *Service) StartPersonSignIn(ref teamsapp.DelegationRef, oid, redirectURI string) (string, string, error) {
	return ref.App.Start(ref.BotID.String()+"|"+oid, redirectURI, Scope)
}

// FinishPersonSignIn completes it. The subject names the bot and the person
// who wrote; the Microsoft account that signed in has to be that person, in
// the bot's tenant.
func (s *Service) FinishPersonSignIn(ctx context.Context, state, cookieNonce, code, redirectURI string) (uuid.UUID, string, error) {
	// The bot is named inside the signed state, but the state's key is the
	// bot's own app — so read the claimed bot first, then let its app check
	// the signature.
	bot, err := botOfState(state)
	if err != nil {
		return uuid.Nil, "", err
	}
	ref, err := s.Bots.DelegationByBot(ctx, bot)
	if err != nil {
		return uuid.Nil, "", msauth.ErrState
	}
	sub, tok, claims, err := ref.App.Finish(ctx, state, cookieNonce, code, redirectURI, Scope)
	if err != nil {
		return uuid.Nil, "", err
	}
	sBot, oid, ok := strings.Cut(sub, "|")
	if !ok || sBot != bot.String() {
		return uuid.Nil, "", msauth.ErrState
	}
	if !strings.EqualFold(claims.Tid, ref.Tenant) || !strings.EqualFold(claims.Oid, oid) {
		return uuid.Nil, "", fmt.Errorf("teamsuser: signed in as %s — sign in as the person who wrote to the bot", claims.Who())
	}
	return bot, oid, s.keepPerson(ctx, bot, oid, tok)
}

func botOfState(state string) (uuid.UUID, error) {
	sub, err := msauth.PeekSubject(state)
	if err != nil {
		return uuid.Nil, err
	}
	b, _, ok := strings.Cut(sub, "|")
	if !ok {
		return uuid.Nil, msauth.ErrState
	}
	id, err := uuid.Parse(b)
	if err != nil {
		return uuid.Nil, msauth.ErrState
	}
	return id, nil
}

func (s *Service) keepPerson(ctx context.Context, bot uuid.UUID, oid string, tok msauth.Tokens) error {
	if tok.RefreshToken == "" || tok.AccessToken == "" {
		return errors.New("teamsuser: Microsoft returned no refresh token (offline_access missing?)")
	}
	g := graphCall{s: s, token: tok.AccessToken}
	var me struct {
		ID                string `json:"id"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := g.get(ctx, "/me?$select=id,userPrincipalName", &me); err != nil {
		return err
	}
	if !strings.EqualFold(me.ID, oid) {
		return errors.New("teamsuser: the token belongs to another person than the one who wrote")
	}
	nonce, ct, err := s.Box.Seal(personAAD(bot, oid), tok.RefreshToken)
	if err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO teamsuser_people (bot_id, oid, ms_user, nonce, ciphertext)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bot_id, oid) DO UPDATE SET ms_user = EXCLUDED.ms_user, nonce = EXCLUDED.nonce,
		  ciphertext = EXCLUDED.ciphertext, updated_at = now()`, bot, oid, me.UserPrincipalName, nonce, ct); err != nil {
		return err
	}
	s.mu.Lock()
	s.people[personKey(bot, oid)] = cached{token: tok.AccessToken, until: until(tok)}
	s.mu.Unlock()
	return nil
}

// SetTaskPerson records whose message a task is working on.
func (s *Service) SetTaskPerson(ctx context.Context, taskID, bot uuid.UUID, oid string) error {
	if s == nil || taskID == uuid.Nil || oid == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO teams_task_people (task_id, bot_id, oid) VALUES ($1, $2, $3)
		ON CONFLICT (task_id) DO UPDATE SET bot_id = EXCLUDED.bot_id, oid = EXCLUDED.oid`, taskID, bot, oid)
	return err
}

// taskPerson answers whose task it is; ok=false for a task no Teams message
// started (a heartbeat, a task from covey itself).
func (s *Service) taskPerson(ctx context.Context, org, taskID uuid.UUID) (bot uuid.UUID, oid string, ok bool, err error) {
	if taskID == uuid.Nil {
		return uuid.Nil, "", false, nil
	}
	var origin string
	var b *uuid.UUID
	var o *string
	err = s.Pool.QueryRow(ctx, `SELECT t.origin, p.bot_id, p.oid FROM backlog_tasks t
		LEFT JOIN teams_task_people p ON p.task_id = t.id WHERE t.id = $1 AND t.org_id = $2`, taskID, org).Scan(&origin, &b, &o)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, "", false, nil
	}
	if err != nil {
		return uuid.Nil, "", false, err
	}
	if b != nil && o != nil {
		return *b, *o, true, nil
	}
	// A task a Teams message started belongs to a person even when the
	// record of who is missing; it must not fall back to the agent's own
	// delegation and act as somebody else.
	if strings.HasPrefix(origin, "webhook:teamsapp") {
		return uuid.Nil, "", true, ErrNoPerson
	}
	return uuid.Nil, "", false, nil
}

// personToken is the person's access token, refreshed through the bot's
// delegation app when it ran out. The bot has to be the organisation's.
func (s *Service) personToken(ctx context.Context, org, bot uuid.UUID, oid string) (string, error) {
	ref, err := s.Bots.DelegationFor(ctx, org)
	if err != nil || ref.BotID != bot {
		return "", ErrNoPerson
	}
	var nonce, ct []byte
	err = s.Pool.QueryRow(ctx, `SELECT nonce, ciphertext FROM teamsuser_people WHERE bot_id = $1 AND oid = $2`, bot, oid).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoPerson
	}
	if err != nil {
		return "", err
	}
	key := personKey(bot, oid)
	s.mu.Lock()
	c, ok := s.people[key]
	s.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.token, nil
	}
	refresh, err := s.Box.Open("teamsuser person", personAAD(bot, oid), nonce, ct)
	if err != nil {
		return "", err
	}
	tok, err := ref.App.Refresh(ctx, refresh, Scope)
	if err != nil {
		return "", fmt.Errorf("%w (%v)", ErrNoPerson, err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if n, c2, err := s.Box.Seal(personAAD(bot, oid), tok.RefreshToken); err == nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE teamsuser_people SET nonce = $3, ciphertext = $4, updated_at = now()
				WHERE bot_id = $1 AND oid = $2`, bot, oid, n, c2)
		}
	}
	s.mu.Lock()
	s.people[key] = cached{token: tok.AccessToken, until: until(tok)}
	s.mu.Unlock()
	return tok.AccessToken, nil
}

// tokenFor picks whose token an action runs with: the person a Teams
// message started the task for, if there is one — and then only theirs —
// otherwise the agent's own delegation (#712).
func (s *Service) tokenFor(ctx context.Context, org, agent, taskID uuid.UUID) (string, error) {
	bot, oid, ok, err := s.taskPerson(ctx, org, taskID)
	if err != nil {
		return "", err
	}
	if ok {
		return s.personToken(ctx, org, bot, oid)
	}
	return s.token(ctx, org, agent)
}

// KeepPending keeps a message while its writer signs in.
func (s *Service) KeepPending(ctx context.Context, bot uuid.UUID, oid string, activity json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_pending (bot_id, oid, activity) VALUES ($1, $2, $3)
		ON CONFLICT (bot_id, oid) DO UPDATE SET activity = EXCLUDED.activity, created_at = now()`, bot, oid, activity)
	return err
}

// TakePending returns and removes the kept message, if one is younger than a
// day.
func (s *Service) TakePending(ctx context.Context, bot uuid.UUID, oid string) (json.RawMessage, bool) {
	var raw json.RawMessage
	err := s.Pool.QueryRow(ctx, `DELETE FROM teamsapp_pending WHERE bot_id = $1 AND oid = $2
		AND created_at > now() - interval '1 day' RETURNING activity`, bot, oid).Scan(&raw)
	return raw, err == nil
}
