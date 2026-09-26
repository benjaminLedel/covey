-- Push notifications (#379).

-- A device that wants to be told: its APNs token, where it registered from,
-- and the language its notifications are written in.
CREATE TABLE push_devices (
    token       text PRIMARY KEY,
    human_id    uuid NOT NULL REFERENCES humans(id) ON DELETE CASCADE,
    platform    text NOT NULL CHECK (platform IN ('ios', 'macos')),
    environment text NOT NULL CHECK (environment IN ('production', 'development')),
    lang        text NOT NULL DEFAULT 'en',
    created_at  timestamptz NOT NULL DEFAULT now(),
    seen_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX idx_push_devices_human ON push_devices(human_id);

-- Where the notifier has read up to. One row; whoever holds its lock does
-- the round, so two processes never push the same entry.
CREATE TABLE push_cursor (
    id int PRIMARY KEY CHECK (id = 1),
    at timestamptz NOT NULL
);
INSERT INTO push_cursor (id, at) VALUES (1, now());

-- What has been pushed, by entry: a result whose task changes again later
-- is not announced twice.
CREATE TABLE push_sent (
    key text PRIMARY KEY,
    at  timestamptz NOT NULL DEFAULT now()
);

-- Whether a notification may carry the first line of what was said. Off:
-- only who and what, and nothing of the content leaves the instance.
ALTER TABLE organizations ADD COLUMN push_preview boolean NOT NULL DEFAULT false;
