-- Back to three kinds. Existing tool requests would violate the condition,
-- so they become findings — they stay readable, but lose
-- their kind. Deleting would be the worse way: it is the list of what
-- the workforce lacks at its workplaces.
UPDATE improvement_items SET kind='finding' WHERE kind='tool_request';
ALTER TABLE improvement_items DROP CONSTRAINT improvement_items_kind_check;
ALTER TABLE improvement_items ADD CONSTRAINT improvement_items_kind_check
    CHECK (kind IN ('proposal','finding','issue'));
