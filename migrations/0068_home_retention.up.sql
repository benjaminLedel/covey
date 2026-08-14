-- Aufbewahrung im Home-Store, und was ein Sync gekostet hat (spec/16,
-- "Retention" und "Interface").
--
-- Ein Speicher, der im Hintergrund still waechst und dessen Inhalt niemand
-- sieht, ist ein Betriebsrisiko — man merkt ihn, wenn die Platte voll ist.
-- Deshalb gehoeren beide Regeln in die Oberflaeche und nicht nur in eine
-- Umgebungsvariable.
--
-- Der juengste Snapshot jedes Agenten bleibt immer, auch wenn beide Regeln ihn
-- fassen wuerden: eine Aufbewahrung, die einem Agenten sein letztes Home nimmt,
-- ist ein Loeschbefehl auf Umwegen. Das steht im Code (ApplyRetention), nicht
-- als CHECK hier — es ist eine Regel ueber das Ergebnis, nicht ueber die Werte.
ALTER TABLE organizations
    -- Wie viele Snapshots je Agent bleiben. 0 = keine Grenze nach Anzahl.
    ADD COLUMN home_retention_keep INTEGER NOT NULL DEFAULT 10,
    -- Hoechstalter in Tagen. 0 = keine Grenze nach Alter.
    ADD COLUMN home_retention_days INTEGER NOT NULL DEFAULT 30;

-- Wie lange der Sync gelaufen ist. Die Zahl beantwortet die einzige Frage, die
-- man an den Schlafpfad hat: ob er teuer ist.
ALTER TABLE home_snapshots ADD COLUMN duration_ms INTEGER NOT NULL DEFAULT 0;
