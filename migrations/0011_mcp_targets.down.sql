DROP TABLE IF EXISTS agent_target_tools;

-- Reset the CHECKs to the state of 0008 (only builtin|custom, custom
-- needs manifest). mcp rows must be removed beforehand.
DO $$
DECLARE c text;
BEGIN
    FOR c IN SELECT conname FROM pg_constraint
        WHERE conrelid = 'target_plugins'::regclass AND contype = 'c'
    LOOP
        EXECUTE format('ALTER TABLE target_plugins DROP CONSTRAINT %I', c);
    END LOOP;
END $$;

ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_kind_check
    CHECK (kind IN ('builtin', 'custom'));
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_manifest_check
    CHECK (kind <> 'custom' OR manifest IS NOT NULL);
