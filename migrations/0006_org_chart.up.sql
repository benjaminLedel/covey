-- Org-Chart (spec/02, spec/09): Vorgesetzten-Beziehungen werden echte
-- Referenzen. Menschen berichten an Menschen (manager_id), Agenten an ihren
-- Vorgesetzten (supervisor_id). Das bisherige Freitext-Feld agents.supervisor
-- war nie beschreibbar und entfällt zugunsten der Referenz.

ALTER TABLE humans ADD COLUMN manager_id UUID REFERENCES humans(id) ON DELETE SET NULL;

ALTER TABLE agents DROP COLUMN supervisor;
ALTER TABLE agents ADD COLUMN supervisor_id UUID REFERENCES humans(id) ON DELETE SET NULL;
