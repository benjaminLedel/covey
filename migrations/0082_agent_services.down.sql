-- The declarations go with the column. There is nothing to save them into: a
-- Covey without this column has no place a service could be named, and the
-- containers themselves are scratch by definition — they end with the sandbox
-- that carried them.
ALTER TABLE agents DROP COLUMN services;
