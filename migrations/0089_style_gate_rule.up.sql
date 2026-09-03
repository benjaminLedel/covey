-- A fifth guard-rail type: style_gate measures the free text of an action
-- against the agent's TONE.md before the action runs (spec/06).
ALTER TABLE guardrails DROP CONSTRAINT guardrails_rule_type_check;
ALTER TABLE guardrails ADD CONSTRAINT guardrails_rule_type_check
    CHECK (rule_type IN ('deny_system','deny_action','require_approval','budget_limit','style_gate'));
