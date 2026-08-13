-- Der zuletzt gueltige Katalog, damit ein Neustart ihn nicht vergisst.
--
-- Bisher lag er nur im Speicher des Prozesses. Das trug den laufenden Betrieb,
-- hatte aber zwei Loecher: nach einem Neustart ist der Cache leer, und wenn der
-- fremde Server in genau diesem Moment nicht antwortet, ist die Store-Seite
-- leer statt veraltet — obwohl die Instanz den Katalog seit Wochen kennt. Und
-- der allererste Aufruf nach dem Start wartet auf einen Server, der irgendwo im
-- Internet steht.
--
-- Der Cache haengt an der URL, nicht an der Organisation: welcher Katalog gilt,
-- entscheidet die Instanz (COVEY_MARKETPLACE_URL). Zeigt jemand die Instanz
-- woanders hin, entsteht eine zweite Zeile, und die alte verfaellt einfach.
CREATE TABLE marketplace_cache (
    url        TEXT PRIMARY KEY,
    body       BYTEA NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
