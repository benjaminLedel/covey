-- Which images may run beside a sandbox, per organisation (spec/16, #121).
--
-- A service is an image reference, and an image reference decides which
-- foreign code runs on the runner host. Inside the sandbox that decision is
-- already the agent's — it runs shell commands there, without root and behind
-- the egress allowlist. A service container is not inside it: it is a second
-- container beside it, from an image nobody looked at, with the host's memory
-- and no accounting.
--
-- So the line is not "may an agent name an image" but "which images may run
-- here at all". This table is that answer, and it holds for every path: the
-- declaration a manager types as much as the one an agent derives from a
-- project's compose file. Whoever may EXTEND the list is the privileged party;
-- naming an image is not, once the list stands.
--
-- Fail-closed — an empty list allows nothing. That is right for a fresh
-- installation and wrong for an upgrade, so the seed below takes what agents
-- already declare. An instance that had services yesterday keeps them, and the
-- list it wakes up with is a description of its own state rather than somebody
-- else's idea of a sensible default.
CREATE TABLE service_image_allow (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    pattern    TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_id, pattern)
);

CREATE INDEX idx_service_image_allow_org ON service_image_allow (org_id);

-- The seed: every image an agent of this organisation already declares, as the
-- repository with any tag. Deliberately the repository and not the exact
-- reference — `postgres:16` becomes `postgres:17` in three months, and a list
-- of pinned tags is one somebody stops maintaining.
--
-- The tag separator is the last colon AFTER the last slash: a registry with a
-- port (`registry.local:5000/db`) carries a colon that is not one. A reference
-- pinned by digest keeps its repository too.
INSERT INTO service_image_allow (org_id, pattern, note)
SELECT DISTINCT a.org_id,
       CASE
         WHEN position('@' in img.image) > 0
           THEN split_part(img.image, '@', 1) || '@*'
         WHEN position(':' in reverse(split_part(img.image, '/', -1))) > 0
           THEN left(img.image, length(img.image) - position(':' in reverse(img.image))) || ':*'
         ELSE img.image || ':*'
       END,
       'carried over at the upgrade: an agent already declared this image'
FROM agents a
CROSS JOIN LATERAL jsonb_to_recordset(a.services) AS img(image TEXT)
WHERE jsonb_array_length(a.services) > 0 AND img.image IS NOT NULL AND img.image <> ''
ON CONFLICT (org_id, pattern) DO NOTHING;
