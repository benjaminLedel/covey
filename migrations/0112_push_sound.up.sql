-- The notification sound a device chose (#381): one of covey's families,
-- the system's sound, or none.
ALTER TABLE push_devices
    ADD COLUMN sound text NOT NULL DEFAULT 'bot' CHECK (sound IN ('bot', 'schar', 'glas', 'system', 'none'));
