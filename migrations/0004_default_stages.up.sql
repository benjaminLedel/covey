-- Jeder Agent soll immer ein Default-Board haben. Neue Agenten bekommen die
-- Spalten beim Anlegen (SeedDefaultStages); dieses Backfill zieht bestehende
-- Agenten nach, die noch gar keine Stage haben. Idempotent über das
-- NOT EXISTS: Agenten mit eigenen Spalten bleiben unangetastet.
INSERT INTO agent_stages (id, agent_id, name, position, color)
SELECT gen_random_uuid(), a.id, d.name, d.position, d.color
FROM agents a
CROSS JOIN (VALUES
    ('Backlog',   0, 'var(--text-muted)'),
    ('In Arbeit', 1, 'var(--text-accent)'),
    ('Erledigt',  2, 'var(--text-success)')
) AS d(name, position, color)
WHERE NOT EXISTS (SELECT 1 FROM agent_stages s WHERE s.agent_id = a.id);
