-- Skills: Fähigkeiten eines Agenten als eigenes Objekt, nicht als Prompt-Text.
--
-- Bisher landete jede Zeile der Agenten-Config in JEDEM Lauf im System-Prompt
-- (agents.CompilePrompt). Für Identität und Grenzen ist das richtig — für
-- Prozeduren nicht: Ein Agent mit fünf Playbooks zahlt alle fünf, auch wenn der
-- Lauf nach drei Turns feststellt, dass nichts zu tun ist. Claude Code kennt
-- dafür Skills: Ein Skill-Verzeichnis unter ~/.claude/skills/<name>/ ist mit
-- seiner description immer sichtbar, sein Rumpf lädt erst bei Bedarf. Weil der
-- Daemon den Lauf mit HOME=<Agenten-Home> startet, werden dort abgelegte Skills
-- zu den PERSÖNLICHEN Skills genau dieses Agenten.
--
-- Zwei Ebenen, dem Secrets-Modell (0007) nachgebaut:
--   agent_id IS NULL     → Skill der Org-Bibliothek, für jeden Agenten verlinkbar
--   agent_id IS NOT NULL → Skill, der nur diesem Agenten gehört
CREATE TABLE skills (
    id          UUID PRIMARY KEY,
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    agent_id    UUID REFERENCES agents(id) ON DELETE CASCADE,
    -- name wird zum Verzeichnisnamen und damit zum /slash-command. Deshalb eng
    -- gehalten: [a-z0-9-], geprüft in der Anwendung (internal/skills).
    name        TEXT NOT NULL,
    -- description ist das einzige, was IMMER im Kontext steht. Sie entscheidet,
    -- ob Claude den Skill lädt — steht deshalb als Spalte hier und nicht im
    -- Datei-Rumpf: Listen und UI brauchen sie, ohne die Dateien zu lesen.
    description TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Eindeutigkeit getrennt je Ebene: Ein Agent darf einen eigenen Skill haben,
-- der genauso heißt wie einer aus der Bibliothek — beim Materialisieren gewinnt
-- dann der eigene (siehe internal/skills.ForAgent).
CREATE UNIQUE INDEX uq_skills_org   ON skills (org_id, name) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_skills_agent ON skills (org_id, agent_id, name) WHERE agent_id IS NOT NULL;

-- Ein Skill ist ein VERZEICHNIS, keine einzelne Datei: SKILL.md plus optionales
-- Beiwerk (Referenztabellen, Vorlagen, Beispiele), auf das der Rumpf verweist.
-- Genau davon lebt das Feature — das Schwergewicht liegt in den Zusatzdateien
-- und wird nur gelesen, wenn der Skill wirklich zieht.
--
-- path ist relativ zum Skill-Verzeichnis. Die Anwendung lehnt absolute Pfade
-- und "..": ab, bevor irgendetwas auf Platte geschrieben wird.
CREATE TABLE skill_files (
    skill_id   UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    content    TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, path)
);

-- Verlinkung von Bibliotheks-Skills an Agenten — dieselbe Regel wie bei
-- Secrets (secret_assignments, siehe secrets/builtin.Resolve): Ohne Zuweisung
-- erreicht ein Bibliotheks-Eintrag keinen Agenten. Für Skills trägt die Regel
-- zusätzlich die Kostenseite: Jeder verfügbare Skill hält seine Beschreibung
-- dauerhaft im Kontext, und ein Recherche-Agent braucht die Deploy-Checkliste
-- nicht. Explizit statt implizit.
CREATE TABLE skill_assignments (
    skill_id   UUID NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, agent_id)
);

CREATE INDEX idx_skill_assignments_agent ON skill_assignments (agent_id);
