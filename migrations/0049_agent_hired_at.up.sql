-- Der Entwurf: ein Agent, den es gibt, den aber noch niemand eingestellt hat.
--
-- Technisch haette der Kill-Switch gereicht — ein toter Agent laeuft ebenfalls
-- nicht. Er wuerde aber zwei verschiedene Tatsachen in dasselbe Feld legen:
-- "der wurde gestoppt" und "der hat noch nicht angefangen". Und das Einstellen
-- ist keine Fahne, sondern ein Zeitpunkt: er steht spaeter im Mitarbeiterprofil
-- neben dem eines Menschen. Details in spec/20-hiring-and-setup.md.
--
-- NULL = Entwurf: wird nicht dispatcht, kein Heartbeat, kein scharfer Webhook,
-- keine Sandbox, keine Kosten. Aufgaben duerfen trotzdem im Backlog liegen und
-- warten auf den ersten Tag.
ALTER TABLE agents ADD COLUMN hired_at TIMESTAMPTZ;

-- Niemand wacht als Entwurf auf: alles, was es heute gibt, arbeitet seit seiner
-- Anlage.
UPDATE agents SET hired_at = created_at;

-- Der Dispatcher fragt bei jedem Tick nach offener Arbeit und filtert dabei
-- ueber diese Spalte mit.
CREATE INDEX agents_hired_idx ON agents (hired_at) WHERE hired_at IS NULL;
