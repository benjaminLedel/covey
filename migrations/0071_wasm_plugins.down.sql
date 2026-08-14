DELETE FROM target_plugins WHERE kind = 'wasm';
ALTER TABLE target_plugins DROP CONSTRAINT IF EXISTS target_plugins_kind_check;
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_kind_check
    CHECK (kind IN ('builtin', 'custom', 'mcp'));
