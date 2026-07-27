-- Aufzeichnungstiefe (Recording-Profil, spec/06): der Org-Boden gilt für alle
-- Agenten (Security/Compliance), der Agent-Override kann nur TIEFER (nie unter
-- den Boden). Stufen: minimal < standard < full (full = inkl. Screenshots).
ALTER TABLE organizations ADD COLUMN recording_level text NOT NULL DEFAULT 'standard';

-- NULL = erbt den Org-Boden; ein gesetzter Wert verschärft (effektiv = max).
ALTER TABLE agents ADD COLUMN recording_level text;
