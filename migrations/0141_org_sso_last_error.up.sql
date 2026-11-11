-- The organisation's last failed single sign-on (#734): the provider's
-- message reaches its org admin, who cannot read the server log. A
-- successful sign-in clears it.
ALTER TABLE org_sso ADD COLUMN last_error TEXT NOT NULL DEFAULT '',
                    ADD COLUMN last_error_at TIMESTAMPTZ;
