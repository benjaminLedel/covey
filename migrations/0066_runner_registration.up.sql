-- Registrierung fremder Runner (spec/16).
--
-- Wie bei GitLab zweigeteilt: ein Registrierungs-Token je Organisation, das in
-- der Oberflaeche erzeugt und widerrufen wird, und daraus abgeleitet ein
-- langlebiges Runner-Token je Runner. Gespeichert wird von beiden nur der Hash.
--
-- Das Registrierungs-Token traegt die Organisation, und der Runner erbt sie
-- daraus. Er kann sie nicht wechseln: ein Runner haelt Homes und Daemon-Tokens,
-- und beides ist Eigentum genau eines Mandanten.
CREATE TABLE runner_registration_tokens (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    created_by  UUID REFERENCES humans(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Widerruf statt Loeschen: wer wissen will, mit welchem Token ein Runner
    -- hereinkam, soll das auch noch beantworten koennen, nachdem das Token
    -- ungueltig ist.
    revoked_at  TIMESTAMPTZ
);
CREATE INDEX idx_runner_reg_tokens_org ON runner_registration_tokens (org_id, created_at DESC);

-- Woher ein Runner kommt und was er kann. Alles nachtraeglich ergaenzt, weil
-- 0051 nur die Identitaet brauchte, gegen die sich der Egress-Proxy meldet.
ALTER TABLE runners
    ADD COLUMN description TEXT NOT NULL DEFAULT '',
    ADD COLUMN tags        TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN version     TEXT NOT NULL DEFAULT '',
    ADD COLUMN arch        TEXT NOT NULL DEFAULT '',
    -- Die Protokollversion, die er spricht. Runner und Server werden getrennt
    -- ausgeliefert, also treffen sich zwangslaeufig verschiedene Staende; die
    -- Ansicht soll den Versatz zeigen koennen, statt ihn vermuten zu lassen.
    ADD COLUMN protocol    INTEGER NOT NULL DEFAULT 0;
