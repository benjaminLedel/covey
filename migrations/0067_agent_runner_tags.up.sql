-- Which runner an agent needs (spec/16, "Scheduling").
--
-- Tags are the host's capability claim: arm64, gpu, a runner on the network
-- of the target system. An agent that needs one of them names them — and gets
-- only runners that name them all. The other direction does not hold: a runner
-- may carry more than is asked of it.
--
-- Empty is the normal case and means "any runner in the organisation".
ALTER TABLE agents ADD COLUMN runner_tags TEXT[] NOT NULL DEFAULT '{}';
