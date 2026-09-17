-- The sandbox image belongs to the agent, not to the instance (D11 in
-- spec/07, carried out in spec/16-runner.md).
--
-- Until now COVEY_SANDBOX_IMAGE decided for everyone: the mail agent carried
-- along the JVM of the developer agent, and a runner could not say for whom it
-- even qualifies — on it, the image is a capacity statement.
--
-- The value is either a profile name (base, dev) or an image reference.
-- Both in one column, because they answer the same question ("what does this
-- agent run in") and the resolution knows exactly one rule: if it is a known
-- profile, its image applies, otherwise the value itself. Empty = the instance
-- default, so that a configuration without a statement stays what it was.
ALTER TABLE agents ADD COLUMN sandbox_image TEXT NOT NULL DEFAULT '';

-- Existing agents get 'dev' and with it exactly the image they ran in so
-- far — the old covey-sandbox:latest carried PHP, JDK, fvm and uv. Without
-- this line an upgrade would pull the toolchain out from under every
-- running developer agent. Whoever wants his support agent leaner sets it
-- to 'base' in the UI — a decision a human makes, not one a
-- migration makes for him.
UPDATE agents SET sandbox_image = 'dev';
