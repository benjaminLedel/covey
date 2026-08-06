-- Der Index, auf dem die Kennzahlen laufen (spec/17-kpis.md).
--
-- Eine Kennzahl ist eine Zaehlregel ueber die Aktions-Events: "wie oft
-- zammad:reply_external, im Zeitraum X, fuer Agent Y". Die vorhandenen Indizes
-- auf recording_events tragen das nicht — sie sind auf (agent_id, id) und
-- (task_id, id) gebaut, also auf das Bloettern durch eine Aufzeichnung, nicht
-- auf das Aggregieren ueber einen Zeitraum.
--
-- Partiell auf kind='action': die Aktions-Events sind der kleinere Teil der
-- Tabelle (jeder Lauf schreibt viele runtime-Events und wenige Aktionen), und
-- keine Kennzahl fragt je nach etwas anderem. Der Index bleibt damit klein
-- genug, um im Cache zu liegen.
--
-- Der Ausdruck payload->>'action' steht bewusst IM Index und nicht in einer
-- generierten Spalte: ADD COLUMN ... GENERATED schreibt die ganze Tabelle neu
-- und sperrt sie dabei. recording_events waechst nur (es gibt keine Retention,
-- darauf beruht die ganze Historie der Kennzahlen) — auf einer laufenden
-- Instanz waere das ein Wartungsfenster fuer eine Spalte, die niemand liest.
--
-- Kein CONCURRENTLY: der Migrator faehrt jede Migration in einer Transaktion,
-- und darin ist es nicht erlaubt. Vertretbar, weil die Migrationen beim Start
-- von `covey serve` laufen — zu dem Zeitpunkt arbeitet kein Agent, der auf das
-- Schreiben eines Events warten muesste.
CREATE INDEX idx_recording_kpi
    ON recording_events (agent_id, (payload->>'action'), created_at)
    WHERE kind = 'action';
