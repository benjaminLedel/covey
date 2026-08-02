-- Audit-Spur für Verwaltungshandlungen von MENSCHEN.
--
-- Was Agenten tun, steht im Recording (recording_events) — lückenlos, seit dem
-- MVP. Was Menschen an der Plattform tun, stand nirgends: Secrets hinterlegen,
-- Guard-Rails löschen, Rollen ändern, den Notaus auslösen, das
-- Recording-Level senken. Ausgerechnet die letzten beiden sind die Handgriffe,
-- die jemand vor einer Übertretung machen würde.
--
-- Bewusst eine eigene Tabelle statt recording_events: Dort hängt jede Zeile an
-- einem Agenten (agent_id NOT NULL), Verwaltungshandlungen haben aber oft
-- keinen — eine Rollenänderung oder ein neues Secret betrifft die Organisation.
--
-- Bewusst OHNE Request-Bodies: In ihnen stünden Secret-Werte und Passwörter.
-- Festgehalten wird, WER WANN WAS ANGEFASST hat (Methode, Pfad, Ergebnis) —
-- nicht der Inhalt. Der Pfad trägt die IDs, damit bleibt „wer hat Guard-Rail X
-- gelöscht" beantwortbar.
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Der Handelnde. NULL, wenn das Konto später gelöscht wird — die Handlung
    -- bleibt trotzdem stehen, mit der E-Mail als Gedächtnisstütze.
    actor_id    UUID REFERENCES humans(id) ON DELETE SET NULL,
    actor_email TEXT NOT NULL DEFAULT '',
    actor_role  TEXT NOT NULL DEFAULT '',
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    -- Die Client-IP, soweit erkennbar (hinter einem Proxy dessen Adresse).
    client_ip   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Die Abfrage der Ansicht: neueste Einträge einer Organisation.
CREATE INDEX idx_audit_org_time ON audit_log (org_id, id DESC);
