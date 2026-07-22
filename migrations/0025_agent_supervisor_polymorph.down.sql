-- Stellt die Fremdschlüssel-Bindung auf humans wieder her. Agent→Agent-
-- Zuordnungen können dabei nicht bestehen bleiben und werden zuvor gelöst.
UPDATE agents SET supervisor_id = NULL
WHERE supervisor_id IS NOT NULL
  AND supervisor_id NOT IN (SELECT id FROM humans);

ALTER TABLE agents ADD CONSTRAINT agents_supervisor_id_fkey
  FOREIGN KEY (supervisor_id) REFERENCES humans(id) ON DELETE SET NULL;
