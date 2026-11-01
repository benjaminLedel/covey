-- Binary files have no place in a text-only skill_files; dropping the column
-- alone would leave rows with empty content that claim to be text.
DELETE FROM skill_files WHERE data IS NOT NULL;
ALTER TABLE skill_files DROP COLUMN data;
ALTER TABLE skills
    DROP COLUMN metadata,
    DROP COLUMN allowed_tools,
    DROP COLUMN compatibility,
    DROP COLUMN license;
