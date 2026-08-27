-- Zurück auf drei Sorten. Bestehende Werkzeug-Bitten würden die Bedingung
-- verletzen, deshalb werden sie zu Befunden — sie bleiben lesbar, verlieren
-- aber ihre Sorte. Löschen wäre der schlechtere Weg: Es ist die Liste dessen,
-- was der Belegschaft an ihren Arbeitsplätzen fehlt.
UPDATE improvement_items SET kind='finding' WHERE kind='tool_request';
ALTER TABLE improvement_items DROP CONSTRAINT improvement_items_kind_check;
ALTER TABLE improvement_items ADD CONSTRAINT improvement_items_kind_check
    CHECK (kind IN ('proposal','finding','issue'));
