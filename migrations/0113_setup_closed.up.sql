-- Setup can be finished before every card is done (#394): someone closes
-- it, and it leaves the navigation. NULL: open until the cards are done.
ALTER TABLE organizations ADD COLUMN setup_closed_at timestamptz;
