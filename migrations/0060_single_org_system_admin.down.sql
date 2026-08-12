-- Die Ebene wieder einziehen. Wer sie behalten will, vergibt sie danach neu
-- mit `covey system-admin add`.
UPDATE accounts SET platform_role = 'user' WHERE platform_role = 'system_admin';
