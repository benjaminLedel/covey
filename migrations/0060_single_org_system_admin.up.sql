-- Damit eine bestehende Installation nach dem Upgrade noch ihren eigenen
-- Betreiber hat.
--
-- Seit P2 haengt die Mandantenverwaltung an accounts.platform_role und nicht
-- mehr an platform_admin (FR-003, Befund F). Der Backfill aus 0059 vergibt
-- ueberall 'user' — nach dem Upgrade koennte also NIEMAND mehr die Instanz
-- verwalten, bis jemand `covey system-admin add` auf dem Server ausfuehrt.
-- Fuer eine selbst betriebene Installation, deren Admin bis gestern beides in
-- einer Person war, ist das eine Aussperrung ohne Ansage.
--
-- Vergeben wird die Ebene deshalb genau dann, wenn sie eindeutig ist: bei
-- EINER Organisation ist deren platform_admin unstrittig auch der Betreiber.
-- Gibt es mehrere, raet diese Migration nicht — dann ist die Frage, wem die
-- Instanz gehoert, eine Entscheidung und keine Ableitung, und der Weg dahin
-- ist `covey system-admin add <mail>`.
UPDATE accounts a SET platform_role = 'system_admin'
WHERE (SELECT count(*) FROM organizations) = 1
  AND a.platform_role = 'user'
  AND EXISTS (
      SELECT 1 FROM humans h
      WHERE h.account_id = a.id AND h.role = 'platform_admin'
  );
