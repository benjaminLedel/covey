-- Ein Secret kann explizit als "einsehbar" markiert werden (z. B. Servernamen,
-- URLs). Dann liefert die Previews-API den vollen Klartext zurück statt des
-- kurzen Präfixes. Passwörter und Tokens bleiben revealed=false.
ALTER TABLE secrets ADD COLUMN revealed BOOLEAN NOT NULL DEFAULT false;
