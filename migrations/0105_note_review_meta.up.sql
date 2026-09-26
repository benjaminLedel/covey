-- What a daily review was written from (#369): its language, the person's
-- time zone ("tz:Europe/Berlin" or "offset:120"), and the end of the last
-- session it covers. With them the instance can tell a review is behind the
-- day's activity and write it again by itself.
ALTER TABLE human_notes
    ADD COLUMN review_lang text NOT NULL DEFAULT '',
    ADD COLUMN review_zone text NOT NULL DEFAULT '',
    ADD COLUMN review_through timestamptz;
