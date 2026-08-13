-- Fallback zwischen Runtimes: wenn der Vertrag, auf dem ein Agent sitzt, gerade
-- erschoepft ist (Session-Limit, Cooldown, kein Slot frei), soll er nicht bis
-- zur naechsten freien Minute liegen bleiben, wenn ein zweiter Vertrag mit
-- anderer Engine bereithsteht (z. B. ein OpenAI-Key neben dem Claude-Code-Abo).
--
-- Bewusst auf der Runtime selbst, nicht am Agenten: "wenn A ausfaellt, nimm B"
-- ist eine Aussage ueber den Vertrag (wie ein Ersatzkontingent), nicht ueber die
-- einzelne Zuweisung. Ein NULL bleibt der Normalfall — Ausfall heisst warten,
-- wie bisher; nur wer explizit eine zweite Engine als Rueckfallebene eintraegt,
-- bekommt das Verhalten.
--
-- ON DELETE SET NULL statt CASCADE: die Fallback-Runtime zu loeschen darf die
-- Haupt-Runtime nicht mitreissen, nur ihr die Rueckfallebene nehmen.
ALTER TABLE runtimes ADD COLUMN fallback_runtime_id UUID REFERENCES runtimes(id) ON DELETE SET NULL;

-- Eine Runtime darf sich nicht selbst als Fallback tragen — das waere ein
-- Kreis der Laenge eins, der beim Erschoepfungs-Check sofort wieder bei sich
-- selbst landet.
ALTER TABLE runtimes ADD CONSTRAINT runtimes_fallback_not_self
    CHECK (fallback_runtime_id IS NULL OR fallback_runtime_id <> id);
