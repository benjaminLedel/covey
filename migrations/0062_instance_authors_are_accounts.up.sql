-- Wer eine Instanz-Sache aendert, ist ein Konto — kein Sitz.
--
-- system_settings.updated_by und waitlist_codes.created_by zeigen seit 0057
-- bzw. 0058 auf humans. Das war richtig, solange jeder Administrator in genau
-- einer Organisation sass. Seit die Instanz-Ebene ausdruecklich KEINE
-- Mitgliedschaft voraussetzt (platformAdmin haengt an auth, nicht an rbac),
-- ist es falsch: der Betreiber ohne Sitz hat keine humans-Zeile, die dort
-- stehen koennte. Sein Schalterwechsel wuerde entweder mit einem
-- Fremdschluesselfehler abbrechen oder als "von niemandem" gebucht.
--
-- Beides sind Autorenspalten, keine Rechte — sie beantworten "wer war das",
-- und die Antwort ist der Mensch hinter der Anmeldung.

ALTER TABLE system_settings DROP CONSTRAINT system_settings_updated_by_fkey;
UPDATE system_settings s SET updated_by = h.account_id
    FROM humans h WHERE h.id = s.updated_by;
ALTER TABLE system_settings ADD CONSTRAINT system_settings_updated_by_fkey
    FOREIGN KEY (updated_by) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE waitlist_codes DROP CONSTRAINT waitlist_codes_created_by_fkey;
UPDATE waitlist_codes c SET created_by = h.account_id
    FROM humans h WHERE h.id = c.created_by;
ALTER TABLE waitlist_codes ADD CONSTRAINT waitlist_codes_created_by_fkey
    FOREIGN KEY (created_by) REFERENCES accounts(id) ON DELETE SET NULL;
