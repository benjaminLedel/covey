-- Eigene Arbeitsplaetze: das Image, das eine Organisation selbst mitbringt.
--
-- Bisher stand es als freier Text am Agenten (agents.sandbox_image). Das hat
-- drei Dinge gekostet, und alle drei fallen mit dieser Tabelle weg:
--
--  1. Es war unsichtbar. Ein Image, das nur in einem Feld an einem Agenten
--     steht, taucht in keiner Uebersicht auf — wer wissen wollte, welche
--     eigenen Images diese Organisation ueberhaupt benutzt, musste jeden
--     Agenten einzeln oeffnen.
--  2. Es war unbeschrieben. Eine Registry-Adresse sagt nicht, was drin ist.
--     Beim naechsten Agenten stand dann dieselbe Adresse noch einmal da, und
--     ob die beiden dasselbe meinen, wusste nur, wer sie gesetzt hat.
--  3. Es war ein Tippfehler weit entfernt vom Fehlschlag: falsch geschrieben
--     faellt es erst beim Wecken auf, in der Aufzeichnung eines Laufs.
--
-- Jetzt ist ein Arbeitsplatz ein benanntes Ding, das man einmal anlegt und
-- danach auswaehlt — genauso wie die Profile aus dem Katalog (spec/16). Der
-- Agent traegt weiterhin nur einen NAMEN; welches Image dahinter liegt,
-- entscheidet die Organisation an einer Stelle.
CREATE TABLE org_workplaces (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Der Name, den ein Agent traegt. Wie ein Profilname aus dem Katalog, und
    -- deshalb im selben Namensraum: Wer `base` heissen will, muesste erklaeren,
    -- welches von beiden gemeint ist. Die Kollision faengt der Server ab
    -- (internal/httpapi), nicht diese Tabelle — sie kennt den Katalog nicht.
    name        TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    -- Wozu dieser Arbeitsplatz da ist. Der Grund, warum die Tabelle mehr ist
    -- als eine Liste von Adressen: Sie beantwortet die Frage, die eine
    -- Registry-Adresse offen laesst.
    description TEXT NOT NULL DEFAULT '',
    -- Die Image-Referenz, so wie docker sie versteht.
    image       TEXT NOT NULL,
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Ein Name gehoert innerhalb einer Organisation genau einem Arbeitsplatz.
CREATE UNIQUE INDEX idx_org_workplaces_name ON org_workplaces (org_id, name);
