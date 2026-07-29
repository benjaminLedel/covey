-- Request-Log: die HTTP-Requests an den Rändern der Plattform (spec/06).
--
-- Das Recording (recording_events) hält fest, WAS ein Agent getan hat — welche
-- Aktion mit welchen Params und ob sie glückte. Was auf der Leitung stand,
-- steht dort nicht: der Bot-Connector-Call nach Teams, die Antwort des
-- Zielsystems, der eingehende Webhook, der an der Signaturprüfung scheiterte.
-- Genau das braucht man beim Anbinden eines Zielsystems.
--
-- Deshalb eine eigene, flache Tabelle statt eines weiteren Event-Kinds:
-- eigenes Retention-Fenster (Requests sind Diagnose-Daten, kein Audit-Trail),
-- eigene Indizes und keine Aufblähung der Agenten-Timeline.
--
-- org_id/agent_id/task_id sind NULLbar: ein eingehender Webhook wird auch dann
-- protokolliert, wenn er abgelehnt wird, bevor ein Agent aufgelöst ist —
-- gerade dieser Fall ist der interessante.
CREATE TABLE request_log (
    id          bigserial PRIMARY KEY,
    created_at  timestamptz NOT NULL DEFAULT now(),
    org_id      uuid,
    agent_id    uuid,
    task_id     uuid,
    -- direction: 'in'  = Covey hat den Request empfangen (Webhook, Trigger)
    --            'out' = Covey hat den Request gestellt (Zielsystem-API)
    direction   text   NOT NULL,
    -- system: Zielsystem-Name des Plugins ('teams', 'zammad', …); leer, wenn
    -- der Request keinem Plugin zuzuordnen ist.
    system      text   NOT NULL DEFAULT '',
    method      text   NOT NULL DEFAULT '',
    url         text   NOT NULL DEFAULT '',
    status      int    NOT NULL DEFAULT 0,
    duration_ms bigint NOT NULL DEFAULT 0,
    req_bytes   bigint NOT NULL DEFAULT 0,
    resp_bytes  bigint NOT NULL DEFAULT 0,
    -- Bodies sind gekappt (erste ~8 KiB) und redigiert (Tokens, Passwörter).
    req_body    text   NOT NULL DEFAULT '',
    resp_body   text   NOT NULL DEFAULT '',
    error       text   NOT NULL DEFAULT '',
    remote      text   NOT NULL DEFAULT ''
);

-- Die Liste liest immer „neueste zuerst", optional nach System/Richtung
-- gefiltert; das Pruning läuft über das Alter.
CREATE INDEX request_log_id_desc_idx ON request_log (id DESC);
CREATE INDEX request_log_system_idx ON request_log (system, id DESC);
CREATE INDEX request_log_created_idx ON request_log (created_at);
