package org

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

// VoiceProvider is the organisation's voice provider (#497): the
// OpenAI-compatible speech server the control plane asks for speech when a
// call speaks an agent's replies. It is the only source of covey's voices;
// without one, the apps speak with the system's synthesis. The key is an
// organisation secret (VoiceProviderKey), not part of this.
//
// Stored in organizations.speech_server (migration 0126): the column kept
// the name it was introduced with.
type VoiceProvider struct {
	// BaseURL is the organisation's own server; empty falls back to its
	// educa AI endpoint when it holds an educa token.
	BaseURL string `json:"base_url,omitempty"`
	// Model and Voice are the defaults when a request names none.
	Model string `json:"model,omitempty"`
	Voice string `json:"voice,omitempty"`
	// Transcribe lets the app send a call's audio to the provider for
	// recognition (#498). Off unless an admin turns it on: the audio then
	// leaves the device.
	Transcribe bool `json:"transcribe,omitempty"`
	// TranscribeModel is the recognition model; empty means whisper-1.
	TranscribeModel string `json:"transcribe_model,omitempty"`
}

// VoiceProviderKey is the name of the organisation secret that holds the
// voice provider's bearer key.
const VoiceProviderKey = "voice_provider_key"

// Configured reports whether an own server is set.
func (v VoiceProvider) Configured() bool { return v.BaseURL != "" }

// VoiceProvider reads the organisation's voice provider; empty when none.
func (s *Store) VoiceProvider(ctx context.Context, id uuid.UUID) (VoiceProvider, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, `SELECT speech_server FROM organizations WHERE id=$1`, id).Scan(&raw); err != nil {
		return VoiceProvider{}, err
	}
	var out VoiceProvider
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// SetVoiceProvider stores it. The caller validates.
func (s *Store) SetVoiceProvider(ctx context.Context, id uuid.UUID, v VoiceProvider) error {
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
