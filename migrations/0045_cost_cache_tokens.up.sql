-- The cached input side of billing.
--
-- input_tokens counts only what did NOT come from the prompt cache. With
-- Claude Code practically the whole context sits in the cache, so the column
-- stayed in the low three-digit range while the same run read millions of
-- tokens. Measured on a single run of tester-1: 56 input_tokens against
-- 2.341.568 cache_read_input_tokens. The cost column was right (it arrives
-- whole from Claude Code), the token column was off by three orders of magnitude.
--
-- Deliberately two own columns instead of an addition onto input_tokens: the
-- three kinds cost different things (a cache read a tenth of fresh input,
-- writing the cache a quarter more). Whoever wants to recompute prices later
-- needs them apart; whoever only wants "how much did the model read" adds them.
--
-- Old records stay at 0: there is no source from which the cache shares of past
-- runs could be reconstructed. The view has to cope with old rows carrying only
-- input_tokens.
ALTER TABLE cost_entries
    ADD COLUMN cache_read_tokens     BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN cache_creation_tokens BIGINT NOT NULL DEFAULT 0;
