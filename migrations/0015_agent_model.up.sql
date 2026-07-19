-- Modell je Agent (z. B. claude-opus-4-8, claude-sonnet-5). Leer = die
-- Runtime entscheidet selbst (Default des Binaries bzw. Accounts).
ALTER TABLE agents ADD COLUMN model TEXT NOT NULL DEFAULT '';
