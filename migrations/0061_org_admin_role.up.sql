-- Die oberste Organisations-Rolle heisst org_admin, nicht platform_admin.
--
-- Seit 0058 gibt es zwei Ebenen: accounts.platform_role ist die Instanz
-- (user | system_admin), humans.role ist der Sitz in einer Organisation. Dass
-- die oberste Org-Rolle weiterhin 'platform_admin' hiess, hat beide Ebenen im
-- selben Wort zusammengezogen — die Oberflaeche zeigte "Plattform-Admin" fuer
-- etwas, das mit der Plattform nichts zu tun hat und das sich jede
-- Organisation selbst vergibt (FR-003, Befund F).
--
-- Ab hier gehoert das Wort "Plattform" der Instanz, "org" der Organisation.

-- Der alte Wert bleibt eine Release lang erlaubt. Nicht fuer die Daten — die
-- werden gleich umgeschrieben — sondern fuer den Fall, dass waehrend eines
-- rollenden Deploys noch ein altes Binary schreibt: dessen INSERT soll an
-- einer Rollenpruefung scheitern koennen, nicht an einem CHECK, der die
-- Transaktion zerreisst. Die Lesekante normalisiert den Wert
-- (identity.NormalizeRole); eine spaetere Migration wirft ihn aus dem CHECK.
ALTER TABLE humans DROP CONSTRAINT humans_role_check;
ALTER TABLE humans ADD CONSTRAINT humans_role_check
    CHECK (role IN ('org_admin','agent_owner','security','auditor','controlling',
                    'platform_admin'));

UPDATE humans SET role = 'org_admin' WHERE role = 'platform_admin';
