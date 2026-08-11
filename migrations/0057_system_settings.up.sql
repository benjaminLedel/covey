-- Die Einstellungen der Installation selbst — eine Zeile je Einstellung.
--
-- Warum in der Datenbank und nicht in der Umgebung: Covey wird von Dritten
-- selbst betrieben (README). Eine Einstellung, die es nur als ENV gibt, kann
-- nur aendern, wer die Unit-Datei bearbeiten und den Prozess neu starten darf
-- — auf einer betriebenen Instanz ist das niemand, der gerade wach ist. Was
-- ein Administrator bedient, gehoert deshalb ins Produkt; in der Umgebung
-- bleibt nur, was noetig ist, um ueberhaupt an diese Tabelle zu kommen
-- (COVEY_DATABASE_URL, COVEY_MASTER_KEY, Adresse, Sandbox-Provider).
--
-- Eine Zeile je Schluessel statt einer breiten Tabelle: ein neuer Schalter ist
-- dann eine Zeile und keine Migration. Fehlt die Zeile, gilt die im Code
-- hinterlegte Vorgabe — eine frische Datenbank braucht also keine Aussaat, und
-- eine bestehende Installation bekommt beim Upgrade genau das, was der Code
-- sagt (signup.mode = off).
--
-- nonce/ciphertext tragen die geheimen Werte (das SMTP-Passwort), versiegelt
-- mit demselben AES-GCM-Verfahren wie die Secrets. Sie bleiben leer, solange
-- nur Klartext-Einstellungen gesetzt sind.
--
-- Die Nummer springt von 0050 auf 0057, weil 0051-0056 in den offenen Zweigen
-- (Runner, Aufgaben-Wiederholung) vergeben sind. Eine Luecke ist harmlos, eine
-- doppelte Nummer nicht: die zweite gilt als laengst angewandt und wird still
-- uebersprungen — die Tabelle entsteht dann nie.
--
-- feature-requests/002-plattform-registrierung.md
CREATE TABLE system_settings (
    key        TEXT PRIMARY KEY,
    value      TEXT,
    nonce      BYTEA,
    ciphertext BYTEA,
    updated_by UUID REFERENCES humans(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
