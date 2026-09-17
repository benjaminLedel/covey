-- Removes only the default columns seeded exactly by the backfill (name + color).
-- Columns created or renamed by a human/agent are kept.
-- Tasks in a removed column fall back to stage_id = NULL via the FK.
DELETE FROM agent_stages
WHERE (name, color) IN (
    ('Backlog',   'var(--text-muted)'),
    ('In Arbeit', 'var(--text-accent)'),
    ('Erledigt',  'var(--text-success)')
);
