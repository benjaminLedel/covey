-- So that an existing installation still has its own operator after the
-- upgrade.
--
-- Since P2 tenant management hangs on accounts.platform_role and no longer on
-- platform_admin (FR-003, finding F). The backfill from 0059 sets 'user'
-- everywhere — after the upgrade NOBODY could manage the instance any more,
-- until someone runs `covey system-admin add` on the server. For a self-hosted
-- installation whose admin was both roles in one person until yesterday, that
-- is a lockout without any warning.
--
-- The tier is granted therefore only where it is unambiguous: with ONE
-- organisation its platform_admin is indisputably also the operator. Where
-- there are several, this migration does not guess — then the question of who
-- owns the instance is a decision and not a derivation, and the way to it is
-- `covey system-admin add <mail>`.
UPDATE accounts a SET platform_role = 'system_admin'
WHERE (SELECT count(*) FROM organizations) = 1
  AND a.platform_role = 'user'
  AND EXISTS (
      SELECT 1 FROM humans h
      WHERE h.account_id = a.id AND h.role = 'platform_admin'
  );
