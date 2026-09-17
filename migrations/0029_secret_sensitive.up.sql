-- Meaning reversed for the protection flag: secrets are viewable variables by
-- default (server names, URLs, config values). Only values explicitly marked
-- "sensitive" (tokens, passwords) are write-only with a prefix preview.
-- The flag applies to org-wide and agent-owned secrets alike.
-- Carry existing data over inverted: secrets hidden so far
-- (revealed=false) stay protected, ones viewable so far stay visible.
ALTER TABLE secrets RENAME COLUMN revealed TO sensitive;
UPDATE secrets SET sensitive = NOT sensitive;
