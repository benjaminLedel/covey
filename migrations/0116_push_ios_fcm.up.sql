-- The iPhone registers with Firebase Messaging (#431): the tokens kept so far
-- are APNs device tokens, which FCM does not take. They go once; the app
-- registers again with an FCM token on its next start.
DELETE FROM push_devices WHERE platform = 'ios';
