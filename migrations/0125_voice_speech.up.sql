-- How the agents carrying a voice sound when their words are spoken (#497):
-- the synthesis model, its speaker and the rate the Mac app uses in a call.
-- NULL leaves it to the app, which picks a voice per agent. Set, not built,
-- like the chat tone (0120) beside it.
ALTER TABLE voices ADD COLUMN speech JSONB;
