-- Ein Runner-Update wird abgelehnt, solange der Host Sandboxen trägt — zu
-- Recht: die Container überstünden den Austausch, ihre Beobachter nicht, und
-- eine Sandbox, die niemand mehr beobachtet, ist schlimmer als ein Update, das
-- wartet. Nur blieb damit das Warten am Menschen hängen: drücken, abgelehnt,
-- später nochmal. Auf einer Produktivinstanz hat das zwei Stunden und drei
-- Anläufe gekostet, während ausgerechnet der Agent, dessen Sandbox das Update
-- blockierte, an dem Fehler litt, den das Update behebt.
--
-- Deshalb steht der Wunsch jetzt an der Zeile des Runners: „auf diese Fassung,
-- sobald du nichts mehr trägst". Auf der Zeile und nicht im Prozess, damit ein
-- Neustart der Steuerebene ihn nicht vergisst.
ALTER TABLE runners ADD COLUMN IF NOT EXISTS update_to TEXT NOT NULL DEFAULT '';
ALTER TABLE runners ADD COLUMN IF NOT EXISTS update_planned_at TIMESTAMPTZ;
