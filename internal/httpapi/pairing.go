package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pairing the mobile app by QR code (#330).
//
// The app needs an API key, and the only way to get one onto a phone was to
// carry it there by hand — through a messenger or a note, which leaves a
// long-lived credential in a third place. A pairing replaces the carrying: the
// web shows a QR code with a code that is good for one use and five minutes,
// the app scans it and exchanges the code for a key of its own.
//
// What it is not: the device badge of spec/27 decision 1. The result is an
// ordinary API key, named after the device, listed and revocable beside the
// others. The pairing changes how a key reaches the phone, not what it is.
//
// Three rules carry it:
//
//   - Creating a code needs the browser session (sessionOnly), exactly like
//     minting a key. A code IS a key a few minutes before it exists, and a
//     leaked key must not be able to pair further devices.
//   - The code is stored as a hash and redeemed atomically: one UPDATE that
//     matches only an unused, unexpired code. Two phones scanning the same
//     code get one key between them.
//   - An unknown, used and expired code get the same answer, so that probing
//     tells nothing.
const (
	pairingPrefix = "coveypair_"
	pairingTTL    = 5 * time.Minute
	// maxDeviceName bounds what the app may call itself; the key's name is
	// "Mobile app · <device>" and has to stay under the 80 of a key name.
	maxDeviceName = 60
)

var errPairingInvalid = errors.New("pairing code invalid, used or expired")

func newPairingCode() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return pairingPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// pairingState is what the page that shows the code reads while it waits.
type pairingState struct {
	ID        uuid.UUID  `json:"id"`
	ExpiresAt time.Time  `json:"expires_at"`
	Redeemed  *time.Time `json:"redeemed_at"`
	Device    string     `json:"device,omitempty"`
}

// handleCreatePairing mints a code for the signed-in seat. The code is in
// this answer and nowhere else; the page puts it into the QR code together
// with its own address, which it knows better than the server does behind a
// proxy.
func (s *Server) handleCreatePairing(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	code, err := newPairingCode()
	if err != nil {
		mapErr(w, err)
		return
	}
	st := pairingState{ID: uuid.New(), ExpiresAt: time.Now().Add(pairingTTL)}
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO device_pairings (id, account_id, human_id, code_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`, st.ID, p.AccountID, p.ID, hashToken(code), st.ExpiresAt); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		pairingState
		Code string `json:"code"`
	}{st, code})
}

// handlePairingState says whether a code of this account has been used, and
// by which device. Another account's pairing is not found, not forbidden.
func (s *Server) handlePairingState(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var st pairingState
	err = s.Pool.QueryRow(r.Context(), `SELECT id, expires_at, redeemed_at, device FROM device_pairings
		WHERE id=$1 AND account_id=$2`, id, principalFrom(r).AccountID).
		Scan(&st.ID, &st.ExpiresAt, &st.Redeemed, &st.Device)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleRedeemPairing is the app's side, and the one route here without a
// badge: the code is the badge. It answers with an API key for the seat that
// created the code.
func (s *Server) handleRedeemPairing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code   string `json:"code"`
		Device string `json:"device"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: expected {\"code\": \"…\", \"device\": \"…\"}")
		return
	}
	code := strings.TrimSpace(body.Code)
	if !strings.HasPrefix(code, pairingPrefix) {
		writeErr(w, http.StatusUnauthorized, errPairingInvalid.Error())
		return
	}
	device := deviceName(body.Device)

	ctx := r.Context()
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var pairingID, accountID, humanID uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE device_pairings SET redeemed_at=now(), device=$2
		WHERE code_hash=$1 AND redeemed_at IS NULL AND expires_at > now()
		RETURNING id, account_id, human_id`, hashToken(code), device).Scan(&pairingID, &accountID, &humanID)
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusUnauthorized, errPairingInvalid.Error())
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}

	token, err := newAPIKeyToken()
	if err != nil {
		mapErr(w, err)
		return
	}
	key := APIKey{ID: uuid.New(), Name: "Mobile app · " + device, Prefix: token[:apiKeyShownPrefix]}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (id, account_id, human_id, name, prefix, token_hash)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at`,
		key.ID, accountID, humanID, key.Name, key.Prefix, hashToken(token)).Scan(&key.CreatedAt); err != nil {
		mapErr(w, err)
		return
	}
	if _, err := tx.Exec(ctx, `UPDATE device_pairings SET api_key_id=$2 WHERE id=$1`, pairingID, key.ID); err != nil {
		mapErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		APIKey
		Token string `json:"token"`
	}{key, token})
}

// deviceName keeps what the app calls itself printable and short. It ends up
// in a key name somebody reads in a list, so control characters and a novel
// have no business in it.
func deviceName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > maxDeviceName {
		s = string(r[:maxDeviceName])
	}
	if s == "" {
		return "unnamed device"
	}
	return s
}
