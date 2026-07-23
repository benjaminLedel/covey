DROP TABLE secret_assignments;
DELETE FROM secrets WHERE agent_id IS NOT NULL;
ALTER TABLE secrets DROP COLUMN agent_id;
ALTER TABLE secrets ADD PRIMARY KEY (org_id, key);
