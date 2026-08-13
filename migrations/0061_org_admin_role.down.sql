UPDATE humans SET role = 'platform_admin' WHERE role = 'org_admin';

ALTER TABLE humans DROP CONSTRAINT humans_role_check;
ALTER TABLE humans ADD CONSTRAINT humans_role_check
    CHECK (role IN ('platform_admin','agent_owner','security','auditor','controlling'));
