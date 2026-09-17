-- Where a plugin came from belongs in the row.
--
-- A manifest plugin was always the same thing until now: somebody uploaded a
-- file. When it comes from a catalogue, three questions stay open that the
-- row cannot answer today: which catalogue, which version, and
-- which digest the instance checked when it installed.
--
-- Without these three there is no update (one would not know which version
-- is installed), no provenance (for the operator who wants to know
-- what came out of the net into their organisation) and no
-- recall (for the withdrawn entry one wants to find again).
--
-- NULL in source means: uploaded by hand. That stays the normal case and
-- the grandfathering — existing rows are exactly that.
ALTER TABLE target_plugins
    ADD COLUMN source         TEXT,
    ADD COLUMN source_version TEXT,
    ADD COLUMN source_digest  TEXT;

-- A catalogue plugin is either fully populated or not at all: a
-- provenance with no version or no digest is a proof that proves nothing.
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_provenance_check
    CHECK (
        (source IS NULL AND source_version IS NULL AND source_digest IS NULL)
        OR (source IS NOT NULL AND source_version IS NOT NULL AND source_digest IS NOT NULL)
    );
