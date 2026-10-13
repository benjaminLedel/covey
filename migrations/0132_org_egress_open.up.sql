-- An organisation may let its agents out to every host (#587). Off by default:
-- the effective allowlist decides, fail-closed, as before.
ALTER TABLE organizations ADD COLUMN egress_open BOOLEAN NOT NULL DEFAULT false;
