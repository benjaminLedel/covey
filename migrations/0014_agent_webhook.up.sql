-- Optional generic webhook trigger per agent: a secret token in the
-- URL (POST /api/trigger/{token}) creates a backlog task and wakes the
-- agent — for systems without their own target system plugin (CI, Cron, Zapier, …).
-- NULL = webhook disabled (default); rotation replaces the token.
ALTER TABLE agents ADD COLUMN webhook_token TEXT UNIQUE;
