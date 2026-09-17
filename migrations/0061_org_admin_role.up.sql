-- The top organisation role is called org_admin, not platform_admin.
--
-- Since 0058 there are two tiers: accounts.platform_role is the instance
-- (user | system_admin), humans.role is the seat in an organisation. That the
-- top org role was still called 'platform_admin' collapsed both tiers into
-- the same word — the UI showed "Plattform-Admin" for something that has
-- nothing to do with the platform and that every organisation grants itself
-- (FR-003, finding F).
--
-- From here on, the word "platform" belongs to the instance, "org" to the organisation.

-- The old value stays allowed for one release. Not for the data — that
-- is rewritten right below — but for the case where an old binary still
-- writes during a rolling deploy: its INSERT should be able to fail a role
-- check, not a CHECK that tears the transaction apart. The read side
-- normalises the value (identity.NormalizeRole); a later migration drops it
-- from the CHECK.
ALTER TABLE humans DROP CONSTRAINT humans_role_check;
ALTER TABLE humans ADD CONSTRAINT humans_role_check
    CHECK (role IN ('org_admin','agent_owner','security','auditor','controlling',
                    'platform_admin'));

UPDATE humans SET role = 'org_admin' WHERE role = 'platform_admin';
