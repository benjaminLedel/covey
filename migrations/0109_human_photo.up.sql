-- A person's profile photo (#377): a medium in the media store, owned by
-- the person. NULL is the monogram.
ALTER TABLE humans ADD COLUMN photo_id uuid;
