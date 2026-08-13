-- Plugins duerfen eigenen Code haben (spec/22).
--
-- Die dritte Art neben builtin und custom/mcp: ein nach WebAssembly
-- uebersetztes Modul. Es reist denselben Weg wie ein Manifest — dieselbe
-- Zeile, dieselbe Aktivierung, derselbe Broker, dieselben Guard-Rails — und
-- liegt aus genau dem Grund auch in derselben Spalte: als
-- {"wasm":"<base64>","describe":{…}} in manifest.
--
-- Base64 in JSONB statt einer eigenen BYTEA-Spalte, weil damit die ganze
-- Kette unveraendert bleibt: BrokeredDefinition, inject_target und der
-- Daemon-Cache kennen weiterhin nur "kind + JSON". Die Aufblaehung um ein
-- Drittel ist der Preis dafuer, und TOAST traegt ihn.
--
-- describe liegt daneben, damit die Store-Liste Name, Beschreibung und
-- Kategorie zeigen kann, ohne jedes Modul zu uebersetzen — Uebersetzen kostet
-- Sekunden, und die Liste wird bei jedem Seitenaufruf gebaut.
ALTER TABLE target_plugins DROP CONSTRAINT IF EXISTS target_plugins_kind_check;
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_kind_check
    CHECK (kind IN ('builtin', 'custom', 'mcp', 'wasm'));
