ALTER TABLE agents DROP COLUMN supervisor_id;
ALTER TABLE agents ADD COLUMN supervisor TEXT NOT NULL DEFAULT '';

ALTER TABLE humans DROP COLUMN manager_id;
