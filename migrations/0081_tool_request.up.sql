-- The fourth kind of open item: the tool request.
--
-- An agent missing a package had no way to say so. It is root nowhere, apt
-- does not run for it, and the workplace stands still until somebody
-- rebuilds an image. What it did instead sat in its home: ~/aptroot with
-- sources.list, resolved package URIs and unpacked .debs, last changed on
-- the day of the observation. Not one afternoon of improvisation but the
-- standing procedure.
--
-- The platform failed its own metaphor here. Covey is the IT department of
-- these employees, and an employee who needs a tool files a request. Because
-- there was no switch for it, it built one in the basement: badly, repeatedly,
-- invisible to everyone.
--
-- Why here and not in a table of its own: it is the same process as a finding
-- (spec/21), no diff, a person decides, the reason for a rejection stays
-- standing. The same person, the same inbox, the same verbs. A second table
-- would be a second, worse inbox.
ALTER TABLE improvement_items DROP CONSTRAINT improvement_items_kind_check;
ALTER TABLE improvement_items ADD CONSTRAINT improvement_items_kind_check
    CHECK (kind IN ('proposal','finding','issue','tool_request'));
