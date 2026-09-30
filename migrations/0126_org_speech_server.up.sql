-- The organisation's speech server (#497, #498): an OpenAI-compatible
-- endpoint (/v1/audio/speech, /v1/audio/transcriptions) the
-- control plane speaks to on behalf of the apps — for a voice that is not
-- synthesised on the device, and, when an admin allows it, for recognising
-- a call's audio. Holds the base URL, the default model and voice and the
-- recognition switch; the key is an organisation
-- secret (speech_server_key) and never stored here. Empty: no own server —
-- an organisation holding an educa AI token speaks through educa AI.
ALTER TABLE organizations ADD COLUMN speech_server JSONB NOT NULL DEFAULT '{}';
