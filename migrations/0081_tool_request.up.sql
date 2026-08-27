-- Die vierte Sorte offener Punkt: die Werkzeug-Bitte.
--
-- Ein Agent, dem ein Paket fehlt, hatte keinen Weg, das zu sagen. Er ist
-- nirgends root, apt ist nicht für ihn, und der Arbeitsplatz steht fest, bis
-- jemand ein Image neu baut. Was er stattdessen tat, lag in seinem Home:
-- ~/aptroot mit sources.list, aufgelösten Paket-URIs und entpackten .debs,
-- zuletzt geändert am Tag der Beobachtung. Kein Nachmittag Improvisation,
-- sondern das stehende Verfahren.
--
-- Das ist die Plattform, die an ihrer eigenen Metapher scheitert: Covey ist die
-- IT-Abteilung dieser Mitarbeiter, und ein Mitarbeiter, der ein Werkzeug
-- braucht, stellt einen Antrag. Weil es keinen Schalter gab, baute er es im
-- Keller — schlecht, wiederholt und für alle unsichtbar.
--
-- Warum hier und nicht in einer eigenen Tabelle: Es ist derselbe Vorgang wie
-- ein Befund (spec/21) — kein Diff, ein Mensch entscheidet, der Grund einer
-- Ablehnung bleibt stehen. Dieselbe Person, derselbe Posteingang, dieselben
-- Verben. Eine zweite Tabelle wäre ein zweiter, schlechterer Posteingang.
ALTER TABLE improvement_items DROP CONSTRAINT improvement_items_kind_check;
ALTER TABLE improvement_items ADD CONSTRAINT improvement_items_kind_check
    CHECK (kind IN ('proposal','finding','issue','tool_request'));
