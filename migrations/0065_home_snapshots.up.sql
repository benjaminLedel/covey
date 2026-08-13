-- Der Home-Store: das Home als Ganzes, nicht als Liste.
--
-- Bisher war das persistente Home ein Verzeichnis neben der Control Plane, und
-- damit war die Bindung eines Agenten an seine Maschine eine Voraussetzung und
-- keine Optimierung. Geht das Verzeichnis verloren, ist die Arbeit weg — von
-- einem gemessenen Entwickler-Home (7,1 GB) existieren 48 MB nirgendwo sonst,
-- und sie liegen ueber das ganze Home verstreut.
--
-- Deshalb wandert nach jedem Job das Home vollstaendig in einen zentralen,
-- inhaltsadressierten Speicher und wird beim Aufwachen daraus materialisiert.
-- Die Frage "was davon ist wertvoll?" wird nie gestellt. Details in
-- spec/16-runner.md, "The central home store".

CREATE TABLE home_snapshots (
    id            UUID PRIMARY KEY,
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id      UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    -- Wo die Arbeitskopie lag, als der Snapshot entstand. ON DELETE SET NULL:
    -- ein abgebauter Runner nimmt seine Arbeitskopie mit, den Snapshot nicht.
    runner_id     UUID REFERENCES runners(id) ON DELETE SET NULL,
    -- Der Snapshot IST dieser Hash: das Manifest liegt selbst als Block im
    -- Speicher, und alles andere hier ist Anzeige.
    manifest_hash TEXT NOT NULL,
    total_size    BIGINT NOT NULL DEFAULT 0,
    -- Was tatsaechlich gewandert ist. Die Differenz zu total_size ist die
    -- eigentliche Aussage: ein 7-GB-Home, aber vielleicht 200 MB, die nur
    -- dieser Agent haelt.
    blocks_up     INTEGER NOT NULL DEFAULT 0,
    bytes_up      BIGINT NOT NULL DEFAULT 0,
    reason        TEXT NOT NULL DEFAULT 'job',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_home_snapshots_agent ON home_snapshots (agent_id, created_at DESC);
CREATE INDEX idx_home_snapshots_org ON home_snapshots (org_id, created_at DESC);

-- Die Vorliebe des Schedulers: dort liegt die Arbeitskopie warm. Ausdruecklich
-- ein Hinweis und kein Versprechen — nichts im System darf annehmen, dass ein
-- Home noch da ist.
ALTER TABLE agents ADD COLUMN last_runner_id UUID REFERENCES runners(id) ON DELETE SET NULL;
