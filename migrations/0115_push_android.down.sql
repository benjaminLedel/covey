DELETE FROM push_devices WHERE platform = 'android';
ALTER TABLE push_devices DROP CONSTRAINT push_devices_platform_check;
ALTER TABLE push_devices ADD CONSTRAINT push_devices_platform_check
    CHECK (platform IN ('ios', 'macos'));
