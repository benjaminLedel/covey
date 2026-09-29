-- How an agent talks in the team chat (#457): address (du | sie | auto), tone
-- (casual | matter_of_fact | formal), emoji (never | sparingly | freely) and a
-- short free text. Part of a voice; the organisation's value is the default
-- for agents without a voice and for fields a voice leaves unset. Validated
-- in internal/voice — the JSON carries only the four keys.
ALTER TABLE voices ADD COLUMN chat_tone JSONB NOT NULL DEFAULT '{}';
ALTER TABLE organizations ADD COLUMN chat_tone JSONB NOT NULL DEFAULT '{}';
