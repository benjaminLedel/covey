-- A secret can be marked explicitly as "viewable" (e.g. server names,
-- URLs). The preview API then returns the full plaintext instead of the
-- short prefix. Passwords and tokens stay revealed=false.
ALTER TABLE secrets ADD COLUMN revealed BOOLEAN NOT NULL DEFAULT false;
