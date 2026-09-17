-- Every agent shall always have a default board. New agents get the columns
-- when they are created (SeedDefaultStages); this backfill pulls existing
-- agents along that have no stage at all. Idempotent through the
-- NOT EXISTS: agents with their own columns stay untouched.
INSERT INTO agent_stages (id, agent_id, name, position, color)
SELECT gen_random_uuid(), a.id, d.name, d.position, d.color
FROM agents a
CROSS JOIN (VALUES
    ('Backlog',   0, 'var(--text-muted)'),
    ('In Arbeit', 1, 'var(--text-accent)'),
    ('Erledigt',  2, 'var(--text-success)')
) AS d(name, position, color)
WHERE NOT EXISTS (SELECT 1 FROM agent_stages s WHERE s.agent_id = a.id);
