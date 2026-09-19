-- Die Triage wird zur Warteschlange.
--
-- Bis hierher lief der Modell-Zug im Request: Wer eine Nachricht abschickte,
-- wartete, bis das Modell entschieden hatte — Sekunden, in denen das
-- Eingabefeld stand und die eigene Nachricht noch nicht einmal im Verlauf zu
-- sehen war. Das ist die falsche Form für einen Chat, und es ist auch die
-- falsche Form für die Aufgabe: Ein Zug, der eine Minute braucht, hängt dann
-- an einer HTTP-Verbindung, die niemand offen halten muss.
--
-- Also bekommt die Nachricht einen eigenen Zustand. Der Request nimmt sie an
-- und ist fertig; die Entscheidung fällt daneben und meldet sich über den
-- Ereignisstrom. Und weil der Zustand in der Datenbank steht und nicht in
-- einer Goroutine, überlebt eine angenommene Nachricht auch einen Neustart:
-- Was beim Hochfahren noch 'pending' ist, wird nachgeholt.
--
--   ''        — nichts zu tun (Triage aus: die Aufgabe steht schon)
--   pending   — angenommen, noch nicht entschieden
--   done      — entschieden
--   failed    — die Entscheidung ist endgültig gescheitert; die Nachricht
--               wurde trotzdem zur Aufgabe, denn eine Nachricht darf nie
--               verschwinden
ALTER TABLE chat_messages ADD COLUMN triage_state TEXT NOT NULL DEFAULT '';

-- Wer beim Hochfahren aufräumt, sucht genau diese Zeilen. Der Teilindex ist
-- winzig, weil er im Normalbetrieb fast leer ist.
CREATE INDEX idx_chat_messages_pending ON chat_messages(created_at)
    WHERE triage_state = 'pending';
