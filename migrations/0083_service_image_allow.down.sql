-- The list goes; the declarations on the agents stay. Without the table there
-- is nothing to enforce, which is the state before this migration — the
-- services then run as they did, governed by who may edit an agent.
DROP TABLE service_image_allow;
