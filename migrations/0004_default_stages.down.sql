-- Entfernt nur die exakt vom Backfill geseedeten Default-Spalten (Name + Farbe).
-- Vom Menschen/Agenten angelegte oder umbenannte Spalten bleiben erhalten.
-- Aufgaben in einer entfernten Spalte fallen per FK auf stage_id = NULL zurück.
DELETE FROM agent_stages
WHERE (name, color) IN (
    ('Backlog',   'var(--text-muted)'),
    ('In Arbeit', 'var(--text-accent)'),
    ('Erledigt',  'var(--text-success)')
);
