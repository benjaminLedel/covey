-- The phase between "the last task is done" and "the sandbox is gone" had no
-- name: the agent kept the status `working` while the platform stopped the
-- container and wrote the home into the store. For a small home that is a
-- second; for a grown one it is half a minute of scanning, and an operator
-- reading the org chart sees an agent apparently busy with a task that
-- finished long ago.
--
-- `securing` names it. The agent is not available for work during it — which
-- is true, and is what the status should say.
ALTER TABLE agents DROP CONSTRAINT IF EXISTS agents_status_check;
ALTER TABLE agents ADD CONSTRAINT agents_status_check
    CHECK (status IN ('sleeping','triggered','triage','working','securing','killed'));
