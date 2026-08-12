-- Reasoning-Aufwand je Agent (low, medium, high, xhigh, max — Claude Codes
-- `--effort`). Leer = die Runtime entscheidet selbst (Default des Binaries).
ALTER TABLE agents ADD COLUMN effort TEXT NOT NULL DEFAULT '';
