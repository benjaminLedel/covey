-- A registration token is for enrolling a host, and enrolling is a moment, not
-- a standing permission. Until now a token was valid until somebody revoked it
-- — and nothing offered the revoke, so a token that leaked into a config repo
-- enrolled runners into the organisation for as long as the row existed (#163).
--
-- Every token now expires; the default is a day, which is far longer than the
-- minutes between creating one and pasting it into `covey-runner register`.
-- Existing tokens get the same day counted from their creation, which makes
-- every token older than that invalid at once — the ones nobody remembers.
ALTER TABLE runner_registration_tokens
    ADD COLUMN expires_at TIMESTAMPTZ;
UPDATE runner_registration_tokens SET expires_at = created_at + interval '1 day';
ALTER TABLE runner_registration_tokens ALTER COLUMN expires_at SET NOT NULL;
