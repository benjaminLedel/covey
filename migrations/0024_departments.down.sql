ALTER TABLE humans DROP COLUMN IF EXISTS department_id;
ALTER TABLE agents DROP COLUMN IF EXISTS department_id;
DROP TABLE IF EXISTS departments;
