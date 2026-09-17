-- Restores the foreign-key binding to humans. Agent→agent
-- assignments cannot survive this and are released beforehand.
UPDATE agents SET supervisor_id = NULL
WHERE supervisor_id IS NOT NULL
  AND supervisor_id NOT IN (SELECT id FROM humans);

ALTER TABLE agents ADD CONSTRAINT agents_supervisor_id_fkey
  FOREIGN KEY (supervisor_id) REFERENCES humans(id) ON DELETE SET NULL;
