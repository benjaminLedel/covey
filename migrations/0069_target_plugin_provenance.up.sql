-- Woher ein Plugin kam, gehoert in die Zeile.
--
-- Ein Manifest-Plugin war bisher immer dasselbe: jemand hat eine Datei
-- hochgeladen. Kommt es aus einem Katalog, sind drei Fragen offen, die die
-- Zeile heute nicht beantworten kann: aus welchem Katalog, welche Version, und
-- welchen Digest hat die Instanz beim Installieren geprueft.
--
-- Ohne diese drei gibt es kein Update (man wuesste nicht, welche Version
-- installiert ist), keinen Herkunftsnachweis (fuer den Betreiber, der wissen
-- will, was aus dem Netz in seine Organisation gelaufen ist) und keinen
-- Rueckruf (fuer den zurueckgezogenen Eintrag, den man wiederfinden will).
--
-- NULL in source heisst: von Hand hochgeladen. Das bleibt der Normalfall und
-- der Bestandsschutz — vorhandene Zeilen sind genau das.
ALTER TABLE target_plugins
    ADD COLUMN source         TEXT,
    ADD COLUMN source_version TEXT,
    ADD COLUMN source_digest  TEXT;

-- Ein Katalog-Plugin ist entweder vollstaendig belegt oder gar nicht: eine
-- Herkunft ohne Version oder ohne Digest waere ein Beleg, der nichts belegt.
ALTER TABLE target_plugins ADD CONSTRAINT target_plugins_provenance_check
    CHECK (
        (source IS NULL AND source_version IS NULL AND source_digest IS NULL)
        OR (source IS NOT NULL AND source_version IS NOT NULL AND source_digest IS NOT NULL)
    );
