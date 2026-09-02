-- What the platform tells a person who is not looking at the tab (#169).
--
-- An outbox and not a channel, for two reasons that only a table can give:
-- a mail that could not go out because SMTP was down has to still be there
-- afterwards, and one that did go out must not go a second time after a
-- restart. Both are questions about a row, not about a goroutine.
--
-- The second reason is the damping. Ten tasks blocked by the same egress rule
-- are one mail with ten lines, not ten mails — so an event is written down
-- when it happens and picked up a few minutes later, together with whatever
-- has joined it in the meantime. due_at is that moment.
CREATE TABLE notifications (
    id         UUID PRIMARY KEY,
    -- The recipient. An ACCOUNT, not a seat: the mail goes to a person, and
    -- the same person may sit in two organisations.
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    -- Which organisation it is about. NULL for what concerns the instance
    -- itself (a runner that is gone, a finding of the doctor).
    org_id     UUID REFERENCES organizations(id) ON DELETE CASCADE,
    -- class groups what is sent TOGETHER and what a person can switch off:
    -- decision | task | cost | ops.
    class      TEXT NOT NULL CHECK (class IN ('decision','task','cost','ops')),
    -- kind is the concrete event within the class (approval, improvement,
    -- task_failed …). It decides how the line reads and whether the thing is
    -- still open when the mail is about to go out.
    kind       TEXT NOT NULL,
    -- subject_id is what the event is about — the approval, the task, the
    -- runner. It is what makes the obsolescence check possible: an approval
    -- decided two minutes after it was raised needs no mail.
    subject_id UUID,
    -- The line as it will stand in the mail. Rendered at emit time, because
    -- that is when the agent's name and the task's title are at hand — and
    -- because a mail must not be wrong just because something was renamed
    -- afterwards.
    title      TEXT NOT NULL,
    -- Where it can be dealt with, as a path (/inbox, /agents/…). The host is
    -- added when sending, from the site address.
    link       TEXT NOT NULL DEFAULT '',
    state      TEXT NOT NULL DEFAULT 'pending'
               CHECK (state IN ('pending','sent','obsolete','failed')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    -- When this may be sent at the earliest — the end of the damping window.
    due_at     TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at    TIMESTAMPTZ
);

-- The sender's query: what is due, per recipient and class.
CREATE INDEX notifications_due_idx ON notifications (account_id, class, due_at)
    WHERE state = 'pending';

-- One row per person and class, and only where somebody has decided against
-- the default. An absent row means the compiled-in default applies — the same
-- mechanism as system_settings, and for the same reason: a new class is then a
-- constant, not a data migration.
CREATE TABLE notification_prefs (
    account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    class      TEXT NOT NULL,
    enabled    BOOLEAN NOT NULL,
    PRIMARY KEY (account_id, class)
);

-- In which language a person is written to. Filled at registration from the
-- page they registered on; empty means the installation's base language.
--
-- It lives on the account and not in a session, because a mail is written when
-- nobody is signed in — that is the whole point of it.
ALTER TABLE accounts ADD COLUMN lang TEXT NOT NULL DEFAULT '';
