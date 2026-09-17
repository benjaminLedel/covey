-- A runner update is refused while the host carries sandboxes — rightly
-- so: the containers would outlive the swap, their observers would not, and
-- a sandbox that nobody observes any more is worse than an update that
-- waits. Only, that left the waiting on the human: press it, refused,
-- later again. On a production instance that cost two hours and three
-- attempts, while the very agent whose sandbox blocked the update
-- suffered from the error that the update fixes.
--
-- That is why the wish now stands on the row of the runner: "to this version,
-- as soon as you carry nothing more". On the row and not in the process, so that a
-- restart of the control plane does not forget it.
ALTER TABLE runners ADD COLUMN IF NOT EXISTS update_to TEXT NOT NULL DEFAULT '';
ALTER TABLE runners ADD COLUMN IF NOT EXISTS update_planned_at TIMESTAMPTZ;
