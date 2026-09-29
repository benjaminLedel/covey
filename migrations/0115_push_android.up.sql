-- Android devices (#424): their FCM token is kept beside the APNs tokens of
-- the iPhones and Macs, and the notifier sends each through its own service.
ALTER TABLE push_devices DROP CONSTRAINT push_devices_platform_check;
ALTER TABLE push_devices ADD CONSTRAINT push_devices_platform_check
    CHECK (platform IN ('ios', 'macos', 'android'));
