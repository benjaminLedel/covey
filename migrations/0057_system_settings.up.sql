-- The settings of the installation itself — one row per setting.
--
-- Why in the database and not in the environment: Covey is self-hosted by
-- third parties (README). A setting that exists only as ENV can only be
-- changed by whoever may edit the unit file and restart the process — on a
-- hosted instance that is nobody who is awake right now. What an
-- administrator operates belongs into the product; the environment keeps only
-- what is needed to reach this table at all
-- (COVEY_DATABASE_URL, COVEY_MASTER_KEY, address, sandbox provider).
--
-- One row per key instead of a wide table: a new switch is then a row and
-- not a migration. If the row is missing, the default held in the code applies
-- — a fresh database therefore needs no seeding, and an existing installation
-- gets on upgrade exactly what the code says
-- (signup.mode = off).
--
-- nonce/ciphertext carry the secret values (the SMTP password), sealed with
-- the same AES-GCM scheme as the secrets. They stay empty as long as only
-- plaintext settings are set.
--
-- The number jumps from 0050 to 0057, because 0051-0056 are taken in the open
-- branches (runner, task repetition). A gap is harmless, a duplicate number is
-- not: the second one counts as long applied and is skipped silently — the
-- table is then never created.
--
-- feature-requests/002-plattform-registrierung.md
CREATE TABLE system_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT,
    nonce      BYTEA,
    ciphertext BYTEA,
    updated_by UUID REFERENCES humans(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
