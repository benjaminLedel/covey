-- Abteilungen tragen eine Akzentfarbe (Hex, z. B. '#7a83cc'); leer = Standard.
ALTER TABLE departments ADD COLUMN color TEXT NOT NULL DEFAULT '';
