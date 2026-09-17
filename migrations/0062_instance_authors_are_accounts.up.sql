-- Whoever changes an instance matter is an account — not a seat.
--
-- system_settings.updated_by and waitlist_codes.created_by have pointed at
-- humans since 0057 and 0058 respectively. That was right as long as every
-- administrator sat in exactly one organisation. Since the instance tier
-- expressly does NOT require membership (platformAdmin hangs on auth, not on
-- rbac), it is wrong: the operator without a seat has no humans row that
-- could stand there. His toggle change would either abort with a foreign key
-- error or be booked as "by nobody".
--
-- Both are author columns, not permissions — they answer "who was that",
-- and the answer is the human behind the login.

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
