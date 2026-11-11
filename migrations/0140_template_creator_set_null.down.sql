ALTER TABLE agent_templates DROP CONSTRAINT agent_templates_created_by_fkey;
ALTER TABLE agent_templates ADD CONSTRAINT agent_templates_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES humans(id);
