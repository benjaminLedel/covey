-- Egress-Allowlist: die über die Oberfläche gepflegten Ziel-Hosts, zu denen
-- Sandboxen ausgehend verbinden dürfen (zusätzlich zu den fest im Code
-- erlaubten Anthropic-Hosts). Plattform-global: der Egress-Proxy ist ein
-- einziger Prozess und kann Verbindungen nicht pro Organisation trennen.
-- Muster: exakter Host ("helpdesk.example.com") oder Wildcard ("*.example.com").
CREATE TABLE egress_allow (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pattern    TEXT NOT NULL UNIQUE,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
