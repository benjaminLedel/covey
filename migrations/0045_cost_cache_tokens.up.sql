-- Die gecachte Eingabeseite der Abrechnung.
--
-- input_tokens zaehlt nur, was NICHT aus dem Prompt-Cache kam. Bei Claude Code
-- liegt praktisch der ganze Kontext im Cache, also blieb die Spalte im niedrigen
-- dreistelligen Bereich, waehrend derselbe Lauf Millionen Tokens las. Gemessen
-- an einem einzelnen Lauf von tester-1: 56 input_tokens gegen 2.341.568
-- cache_read_input_tokens. Die Kostenspalte stimmte (sie kommt fertig von
-- Claude Code), die Token-Spalte war um drei Groessenordnungen daneben.
--
-- Bewusst zwei eigene Spalten statt einer Addition auf input_tokens: die drei
-- Sorten kosten Verschiedenes (ein Cache-Read ein Zehntel frischer Eingabe, das
-- Schreiben des Caches ein Viertel mehr). Wer spaeter Preise nachrechnen will,
-- braucht sie getrennt; wer nur "wieviel hat das Modell gelesen" will, addiert.
--
-- Altbestand bleibt bei 0: Es gibt keine Quelle, aus der sich die Cache-Anteile
-- vergangener Laeufe rekonstruieren liessen. Die Ansicht muss damit umgehen
-- koennen, dass alte Zeilen nur input_tokens tragen.
ALTER TABLE cost_entries
    ADD COLUMN cache_read_tokens     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN cache_creation_tokens BIGINT NOT NULL DEFAULT 0;
