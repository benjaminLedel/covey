-- How a claimed SSO domain was confirmed (#729): by its DNS record, or by hand
-- by an instance system admin, whose account is kept beside it.
ALTER TABLE org_sso_domains
    ADD COLUMN verified_method TEXT CHECK (verified_method IN ('dns', 'manual')),
    ADD COLUMN verified_by     UUID REFERENCES accounts(id) ON DELETE SET NULL;
UPDATE org_sso_domains SET verified_method = 'dns' WHERE verified_at IS NOT NULL;
