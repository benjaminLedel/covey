-- The team surface becomes an opt-in per organisation (#328).
--
-- Off is the default while the surface is in beta: an installation that
-- upgrades keeps the console at the root until somebody in the organisation
-- turns it on. The column is read by /auth/me, which is how the interface
-- picks its shell, and by the message endpoint, which refuses when it is off.
ALTER TABLE organizations ADD COLUMN team_surface BOOLEAN NOT NULL DEFAULT false;
