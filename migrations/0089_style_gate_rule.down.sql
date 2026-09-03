DELETE FROM guardrails WHERE rule_type = 'style_gate';
ALTER TABLE guardrails DROP CONSTRAINT guardrails_rule_type_check;
ALTER TABLE guardrails ADD CONSTRAINT guardrails_rule_type_check
    CHECK (rule_type IN ('deny_system','deny_action','require_approval','budget_limit'));
