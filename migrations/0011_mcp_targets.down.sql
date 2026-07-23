DROP TABLE IF EXISTS agent_target_tools;

-- CHECKs auf den Stand von 0008 zurücksetzen (nur builtin|custom, custom
-- braucht manifest). mcp-Zeilen müssen vorher entfernt sein.
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
