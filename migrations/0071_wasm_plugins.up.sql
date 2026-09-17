-- Plugins may have their own code (spec/22).
--
-- The third kind beside builtin and custom/mcp: a module compiled to
-- WebAssembly. It travels the same road as a manifest — the same
-- row, the same activation, the same broker, the same guard rails — and
-- for that very reason it also lies in the same column: as
-- {"wasm":"<base64>","describe":{…}} in manifest.
--
-- Base64 in JSONB instead of its own BYTEA column, because that keeps the
-- whole chain unchanged: BrokeredDefinition, inject_target and the
-- daemon cache go on knowing only "kind + JSON". The third of bloat is
-- the price for it, and TOAST carries it.
--
-- describe lies beside it, so the store list can show name, description and
-- category without compiling every module — compiling costs
-- seconds, and the list is built on every page view.
ALTER TABLE target_plugins DROP CONSTRAINT IF EXISTS target_plugins_kind_check;
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_kind_check
    CHECK (kind IN ('builtin', 'custom', 'mcp', 'wasm'));
