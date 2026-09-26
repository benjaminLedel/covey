-- A note's icon and cover (#372): an emoji, and a built-in gradient
-- ("gradient:<name>") or a picture of the note's media ("covey-media://<id>").
ALTER TABLE human_notes
    ADD COLUMN icon text NOT NULL DEFAULT '',
    ADD COLUMN cover text NOT NULL DEFAULT '';
