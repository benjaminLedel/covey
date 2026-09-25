-- How densely the office is furnished — as organisation master data, not as
-- a choice in somebody's browser.
--
-- The office (#323) scales the budgets of its room recipes, the pieces along
-- the corridors and the islands in the lounges by one number. Remembered in
-- local storage, every person saw a different house, the choice was lost on
-- another device, and it was made by whoever happened to click. The density
-- is a property of the building, and the building belongs to the
-- organisation: set once by an org admin, seen by everyone (#325).
ALTER TABLE organizations
    -- 'sparse', 'normal' or 'rich'; anything else is refused by the API.
    ADD COLUMN office_furnishing TEXT NOT NULL DEFAULT 'normal';
