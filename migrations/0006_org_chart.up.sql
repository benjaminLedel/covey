-- Org chart (spec/02, spec/09): supervisor relations become real
-- references. Humans report to humans (manager_id), agents to their
-- supervisor (supervisor_id). The old free-text field agents.supervisor was
-- never writable and is dropped in favor of the reference.

ALTER TABLE humans ADD COLUMN manager_id UUID REFERENCES humans(id) ON DELETE SET NULL;

ALTER TABLE agents DROP COLUMN supervisor;
ALTER TABLE agents ADD COLUMN supervisor_id UUID REFERENCES humans(id) ON DELETE SET NULL;
