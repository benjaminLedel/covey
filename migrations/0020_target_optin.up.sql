-- Target system activation becomes opt-in (fail-closed): until now compiled
-- built-ins without a row counted as enabled — that made GitLab/Zammad act
-- like standard everywhere (actions, webhooks, profile-field identifiers). From
-- now on only an explicit row with enabled=TRUE counts. Grandfathering as data instead of a
-- code default: existing organisations get their previously implicit
-- built-ins as an explicit activation; whoever had disabled one keeps
-- its row (ON CONFLICT DO NOTHING).
INSERT INTO target_plugins (org_id, name, kind, enabled)
SELECT o.id, b.name, 'builtin', TRUE
FROM organizations o
CROSS JOIN (VALUES ('zammad'), ('gitlab')) AS b(name)
ON CONFLICT (org_id, name) DO NOTHING;
