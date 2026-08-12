-- Wo der Quelltext dieser Plattform liegt — als Konfiguration, nicht als
-- Entscheidung eines Agenten (spec/21).
--
-- Der Betriebsingenieur macht den wertvollsten Befund dort, wo er ihn nicht
-- durch eine Config beheben kann: drei Agenten sind diese Woche am Turn-Limit
-- gestorben, keiner war falsch konfiguriert — der Plattform fehlt ein Weg, ein
-- Teilergebnis zurueckzugeben. Das ist ein Fehlerbericht, und er ist der
-- Einzige in der Organisation, der alle drei gesehen hat.
--
-- WELCHES Repository, entscheidet die Organisation und nicht der Agent. Eine
-- Instanz, die gegen den oeffentlichen GitHub-Spiegel laeuft, haette sonst
-- einen Agenten, der Issues dorthin schreibt, wo die Welt mitliest; eine
-- Instanz, die in ihr eigenes GitLab meldet, behaelt sie im Haus. Deshalb
-- Stammdaten neben der Unternehmensbeschreibung und kein Prompt-Text: was ein
-- Agent selbst waehlen kann, waehlt er irgendwann anders.
--
-- Dieselbe Adresse traegt beides — das Lesen und das Melden. Wer gegen Code
-- berichtet, den er nicht gelesen hat, meldet Symptome; wer einen anderen
-- Stand liest als den, der laeuft, meldet die Haelfte doppelt und die andere
-- Haelfte zu frueh.
ALTER TABLE organizations
    -- Das Zielsystem, ueber das gelesen und gemeldet wird: der Name des
    -- Plugins, wie er in einer ACCESS.md steht ('gitlab', 'github').
    -- Leer = nicht eingerichtet, und dann steht davon auch nichts im Prompt.
    ADD COLUMN platform_repo_system TEXT NOT NULL DEFAULT '',
    -- Das Projekt dort, in der Schreibweise des Systems: bei GitLab die
    -- numerische ID oder der Pfad, bei GitHub "owner/repo".
    ADD COLUMN platform_repo_project TEXT NOT NULL DEFAULT '';
