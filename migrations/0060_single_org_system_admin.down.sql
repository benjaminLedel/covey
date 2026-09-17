-- Withdraw the tier again. Whoever wants to keep it re-grants it afterwards
-- with `covey system-admin add`.
UPDATE accounts SET platform_role = 'user' WHERE platform_role = 'system_admin';
