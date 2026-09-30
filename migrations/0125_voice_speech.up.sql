-- How the agents carrying a voice sound when a call speaks their words
-- (#497): the voice name at the organisation's voice provider, a short
-- style hint for it and the speed. NULL leaves it to the provider's default
-- voice. Set, not built, like the chat tone (0120) beside it.
ALTER TABLE voices ADD COLUMN speech JSONB;
