-- The last valid catalogue, so that a restart does not forget it.
--
-- Until now it only sat in the memory of the process. That carried running
-- operation, but had two holes: after a restart the cache is empty, and when
-- the foreign server does not answer at exactly that moment, the store page is
-- empty instead of stale — even though the instance has known the catalogue for
-- weeks. And the very first call after a start waits for a server standing
-- somewhere on the internet.
--
-- The cache hangs off the URL, not off the organisation: which catalogue
-- applies is decided by the instance (COVEY_MARKETPLACE_URL). If someone points
-- the instance elsewhere, a second row appears and the old one decays on its own.
CREATE TABLE marketplace_cache (
    url        TEXT PRIMARY KEY,
    body       BYTEA NOT NULL,
    fetched_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
