-- Deleting a person failed once they had created an agent template (#731):
-- agent_templates.created_by was the one foreign key on humans without an
-- ON DELETE clause. The template belongs to the organisation; it only loses
-- who made it, like every other created_by.
ALTER TABLE agent_templates DROP CONSTRAINT agent_templates_created_by_fkey;
ALTER TABLE agent_templates ADD CONSTRAINT agent_templates_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES humans(id) ON DELETE SET NULL;
