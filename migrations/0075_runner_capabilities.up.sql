-- Tags and images of a runner become properties of the control plane instead of
-- the registration. Until now both came out of the host's config.toml, sent
-- once at connect: changing them meant editing a file on the machine and
-- restarting the runner, and nobody could see in the interface why an agent was
-- not being served.
--
-- extra_tags is ADDITIVE to what the runner reports about itself: a tag says
-- what a host IS (arm64, gpu), and the operator adds what it is FOR.
--
-- assigned_images REPLACES the reported claim when it is set — NULL means "the
-- operator has not decided", an empty array means "no claim, this host provides
-- every workplace and fetches it on demand". That difference is the point: it
-- is the only way to take back a claim a register invented.
ALTER TABLE runners
    ADD COLUMN extra_tags      text[] NOT NULL DEFAULT '{}',
    ADD COLUMN assigned_images text[];
