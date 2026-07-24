-- Semantik-Umkehr beim Schutz-Flag: Secrets sind per Default einsehbare
-- Variablen (Servernamen, URLs, Konfigwerte). Nur explizit als "sensibel"
-- markierte Werte (Tokens, Passwörter) sind write-only mit Präfix-Vorschau.
-- Das Flag gilt für org-weite und agent-eigene Secrets gleichermaßen.
-- Bestandsdaten invertiert übernehmen: bisher verborgene Secrets
-- (revealed=false) bleiben geschützt, bisher einsehbare bleiben sichtbar.
ALTER TABLE secrets RENAME COLUMN revealed TO sensitive;
UPDATE secrets SET sensitive = NOT sensitive;
