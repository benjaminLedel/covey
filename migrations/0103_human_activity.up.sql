-- The activity log (#363): what a person worked on, recorded by the macOS
-- app in the background once the person switched it on.
--
-- A session is one stretch in one app, window and page: when it began and
-- ended, the window's title, the page's address, the focused field and an
-- excerpt of its content. Like a note it belongs to a seat (human_id) and to
-- nobody else — no route reads another person's activity, org_admin
-- included: switched on by the person, for the person, never monitoring.
--
-- Rows older than the retention (COVEY_ACTIVITY_RETENTION, default 14 days)
-- are deleted as new ones arrive.
CREATE TABLE human_activity (
    id          uuid PRIMARY KEY,
    org_id      uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    human_id    uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    started_at  timestamptz NOT NULL,
    ended_at    timestamptz NOT NULL CHECK (ended_at >= started_at),
    app         text NOT NULL DEFAULT '',
    bundle      text NOT NULL DEFAULT '',
    window_title text NOT NULL DEFAULT '',
    url         text NOT NULL DEFAULT '',
    field       text NOT NULL DEFAULT '',
    excerpt     text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Read as one person's day, in order; purged by age.
CREATE INDEX idx_human_activity_owner ON human_activity (human_id, started_at);
CREATE INDEX idx_human_activity_age ON human_activity (started_at);
