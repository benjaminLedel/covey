package org

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// SpeechServer is the organisation's own speech server (#497): where the
// control plane asks for speech when a voice is synthesised by the
// organisation rather than on the device. The key is an organisation secret
// (SpeechServerKey), not part of this.
type SpeechServer struct {
	// BaseURL is the organisation's own server; empty falls back to its
	// educa AI endpoint when it holds an educa token.
	BaseURL string `json:"base_url,omitempty"`
	// Model and Voice are the defaults when a request names none.
	Model string `json:"model,omitempty"`
	Voice string `json:"voice,omitempty"`
	// Transcribe lets the app send a call's audio to the server for
	// recognition (#498). Off unless an admin turns it on: the audio then
	// leaves the device.
	Transcribe bool `json:"transcribe,omitempty"`
	// TranscribeModel is the recognition model; empty means whisper-1.
	TranscribeModel string `json:"transcribe_model,omitempty"`
}

// SpeechServerKey is the name of the organisation secret that holds the
// speech server's bearer key.
const SpeechServerKey = "speech_server_key"

// Configured reports whether an own server is set.
func (s SpeechServer) Configured() bool { return s.BaseURL != "" }

// SpeechServer reads the organisation's speech server; empty when none.
func (s *Store) SpeechServer(ctx context.Context, id uuid.UUID) (SpeechServer, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT speech_server FROM organizations WHERE id=$1`, id).Scan(&raw); err != nil {
		return SpeechServer{}, err
	}
	var out SpeechServer
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// SetSpeechServer stores it. The caller validates.
func (s *Store) SetSpeechServer(ctx context.Context, id uuid.UUID, v SpeechServer) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET speech_server=$2 WHERE id=$1`, id, raw)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
