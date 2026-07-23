-- Secrets mit Agent-Scope (spec/04): agent_id NULL = org-weites Secret (wie
-- bisher), gesetzt = agent-eigenes Secret, nur für diesen Agenten auflösbar.
ALTER TABLE secrets DROP CONSTRAINT secrets_pkey;
ALTER TABLE secrets ADD COLUMN agent_id UUID REFERENCES agents(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX uq_secrets_org   ON secrets (org_id, key) WHERE agent_id IS NULL;
CREATE UNIQUE INDEX uq_secrets_agent ON secrets (org_id, agent_id, key) WHERE agent_id IS NOT NULL;

-- Explizite Zuweisung org-weiter Secrets an Agents: ohne Zuweisung gilt ein
-- Org-Secret für alle Agents (Default), mit Zuweisungen nur für die genannten.
CREATE TABLE secret_assignments (
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    agent_id   UUID NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, key, agent_id)
);
