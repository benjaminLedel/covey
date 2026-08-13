-- Zurueck auf humans. Konten ohne Sitz verlieren dabei ihre Urheberschaft —
-- es gibt keine humans-Zeile, auf die sie zeigen koennten.
ALTER TABLE system_settings DROP CONSTRAINT system_settings_updated_by_fkey;
UPDATE system_settings s SET updated_by = (
    SELECT h.id FROM humans h WHERE h.account_id = s.updated_by ORDER BY h.created_at LIMIT 1);
ALTER TABLE system_settings ADD CONSTRAINT system_settings_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES humans(id) ON DELETE SET NULL;

ALTER TABLE waitlist_codes DROP CONSTRAINT waitlist_codes_created_by_fkey;
UPDATE waitlist_codes c SET created_by = (
    SELECT h.id FROM humans h WHERE h.account_id = c.created_by ORDER BY h.created_at LIMIT 1);
ALTER TABLE waitlist_codes ADD CONSTRAINT waitlist_codes_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES humans(id) ON DELETE SET NULL;
