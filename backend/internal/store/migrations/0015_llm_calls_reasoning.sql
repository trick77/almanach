-- A call's reasoning tokens, a part of output_tokens: where a call's
-- output goes, by purpose, is what the costs page compares. NULL when
-- the gateway reported none, and for a call from before.
ALTER TABLE llm_calls ADD COLUMN reasoning_tokens INTEGER CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0);
