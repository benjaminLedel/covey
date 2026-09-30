-- The organisation's voice provider (#497, #498): an OpenAI-compatible
-- speech server (/v1/audio/speech, /v1/audio/transcriptions) the control
-- plane speaks to on behalf of the apps — the one source of the voice a
-- call speaks with, and, when an admin allows it, where a call's audio is
-- recognised. Holds the base URL, the default model and voice and the
-- recognition switch; the key is an organisation secret
-- (voice_provider_key) and never stored here. Empty: no own server — an
-- organisation holding an educa AI token speaks through educa AI.
ALTER TABLE organizations ADD COLUMN speech_server JSONB NOT NULL DEFAULT '{}';
