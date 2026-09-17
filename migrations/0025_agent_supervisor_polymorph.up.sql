-- Until now an agent could only report to a human: agents.supervisor_id
-- carried a foreign key on humans(id) (migration 0006). So that agents can
-- also be subordinate to other agents, this FK binding is dropped.
-- supervisor_id now points polymorphically at a human OR an agent
-- of the same organisation. Referential integrity on delete is
-- maintained in the application instead (agents.Delete and org.DeleteHuman
-- set referencing supervisor_id to NULL).
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_supervisor_id_fkey;
