-- Pairing the mobile app by QR code (#330).
--
-- A pairing is a short-lived, single-use code that the web shows as a QR
-- code and the app exchanges for an API key. Only the hash of the code is
-- stored, like the hash of an API key: whoever reads this table cannot pair a
-- device with it.
--
-- The row outlives its use on purpose: the page that showed the code polls it
-- to say "paired with <device>", and api_key_id says which key came of it.
CREATE TABLE device_pairings (
    id          uuid PRIMARY KEY,
    account_id  uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    human_id    uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    code_hash   text NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    redeemed_at timestamptz,
    device      text NOT NULL DEFAULT '',
    api_key_id  uuid REFERENCES api_keys(id) ON DELETE SET NULL
);
