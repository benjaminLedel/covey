# 08 — Market environment & build vs. adopt

The result of market research (as of July 2026) into open-source and commercial tools that cover covey's conceptual space in whole or in part. The goal: not to build what already exists in mature, self-hostable form — and to concentrate the energy on the seam nobody closes.

## Core finding

covey's concept is **no longer a blank spot**. "Agent control plane" and "AI workforce / AI coworker" are established categories in 2026 with serious competition. The control plane is framed industry-wide as "Kubernetes for AI agents" (not just coordination, but management, governance, observability). And the analogy that carries covey — agent as task-oriented/ephemeral vs. **coworker** as a persistent member of the workforce with a role, skills, tools, a budget and a reporting hierarchy — has been adopted practically everywhere.

The differentiation therefore lies **not in the concept but in a combination nobody else offers**: self-hosted **and** runtime-agnostic **and** for a technical operator (rather than a no-code business user) **and** with a real sandbox-as-a-workplace. Every full platform breaks on at least one of these axes. Two open-source projects come so close to the core, though, that they are more foundation than competition (kagent, OpenHands Agent Canvas — see below).

## The closest full-platform hits

| Platform | Covers | Breaks on (for covey) |
|---|---|---|
| **AWS Bedrock AgentCore** | Runtime, gateway, **identity (token exchange)**, policy, memory, browser/code interpreter (sandbox), observability, evaluations; framework-agnostic (CrewAI, LangGraph, LlamaIndex, Strands …) | AWS lock-in, cloud-only, no self-hosting on your own infra |
| **OpenHands Agent Canvas** (OSS) | Self-hosted **always-on engineering team**; drives OpenHands, Claude Code, Codex, Gemini over **ACP**; multi-backend (local/VM/Docker/cloud); event triggers (Slack/GitHub/Datadog); enterprise: agent control plane, RBAC, budget | Focused on coding/technical agents; no HR/org-chart/secrets-broker/backlog bracket |
| **kagent** (OSS, CNCF, Istio founders) | Agents as **CRDs (GitOps, PR review)**; BYO frameworks; substrate runtime with **isolation per agent**; HITL **approval gates + agent-initiated questions**; long-term memory; OTel | K8s-centric; no org/employee bracket, no secrets broker service, no backlog model |
| **IBM watsonx Orchestrate — Agentic Control Plane** (June 2026) | Central operation/governance/scaling across the whole enterprise environment | Heavyweight, licence-bound, stack-tied |
| **Microsoft Agent 365** (GA May 2026) | Governance control plane, discovers/secures agents everywhere, shadow-AI discovery (Defender/Intune) | Microsoft stack, governance instead of execution |
| **AI workforce SaaS** (Lindy, Relevance AI, Gumloop, OpenAI Frontier, ServiceNow Autonomous Workforce, Atomicwork, Knowlee) | The **employee model** explicitly: hire/onboard/performance review, an identity of its own, permissions, growing memory; fleet cockpits (kanban, shared graph, governance) | Uniformly no-code + closed cloud; too little control for technical operators, not self-hostable |

**OpenAI Frontier** (Feb 2026) and **Knowlee** are conceptually closest to covey's employee framing; both closed. **AgentCore** is the architecturally closest match; AWS-bound.

## Building blocks per layer (open-source first)

Mature, self-hostable building blocks covey assembles itself from instead of inventing them:

### Runtimes (the thing covey wraps)
OpenHands (79k+ stars, self-hostable), Claude Code, Codex, Gemini — addressable uniformly via **ACP (Agent Client Protocol)**. ACP is effectively covey's "daemon protocol/adapter" as an already existing standard. → **Adopt.**

### Sandbox / workplace
- **E2B** (OSS, Firecracker microVMs, a dedicated kernel per session) — the strongest isolation; self-hosting = a real infrastructure project (Nomad/Consul).
- **Beam** (gVisor+runc, AGPL) — simpler to operate; explicitly operable on your own credits (AWS/GCP/Azure/**Hetzner**).
- **Northflank** (BYOC, Kata/gVisor), **OpenSandbox** (CNCF).
- ⚠️ **Daytona** switched its production code to closed source in June 2026; the OSS repo is archived.

→ **Adopt** (not from scratch; see D2 in [`07-open-decisions.md`](07-open-decisions.md)).

### Identity & secrets
RFC 8693 token exchange (with the act claim) is the **industry consensus** — Microsoft, Okta, AWS, Google have converged; Anthropic made workload identity federation GA in June 2026. Ready-made building blocks: **HashiCorp Vault** (dynamic, short-lived credentials), **SPIFFE/SPIRE**, **Aembit**, **Keycloak** (your Girona setup). NHI/posture tools: Astrix, Oasis, Entro, Token Security.

> **Honest consequence:** covey's identity approach is therefore **correct, but table stakes** — no longer a unique selling point. See [`04-identity-secrets.md`](04-identity-secrets.md). → **Adopt** (Keycloak + Vault), do not position it as differentiation.

### Memory
**Graphiti** (Apache 2.0, temporal) leads on temporal accuracy (LongMemEval 63.8 % vs. Mem0 49.0 %); self-hostable on a graph DB. ⚠️ Zep Community Edition is deprecated → self-hosting = directly on the Graphiti library. Alternatives: Mem0 (personalisation), Letta/MemGPT (OS-like self-editing), Cognee (graph+vector from documents). Confirmed for covey's `SOUL.md` philosophy: **a Markdown vault + semantic search** is a valid pattern when humans are to remain first-class authors. See [`05-memory.md`](05-memory.md). → **Adopt** (Graphiti).

### Observability & guard rails
- **Langfuse** (MIT) — tracing, LLM-as-judge, prompt management; fully self-hostable (Postgres/ClickHouse/Redis/S3).
- Guard-rail libraries: **NeMo Guardrails** (Apache), **Guardrails AI** (Apache), **LLM Guard** (MIT — prompt injection, PII, toxicity).
- **Galileo Agent Control** (open-sourced) — a central policy control plane with **hot-reloadable** rules across an entire agent fleet. Conceptually almost identical to covey's guard-rail engine; check it before building your own. See [`06-observability-control.md`](06-observability-control.md). → **Adopt** (Langfuse + LLM Guard; evaluate Galileo Agent Control).

## Build vs. adopt — matrix

| covey layer | Recommendation | Building block |
|---|---|---|
| Runtimes | **Adopt** | OpenHands, Claude Code … via ACP |
| Sandbox | **Adopt** | E2B / Beam (self-hosted) |
| Runtime abstraction / daemon | **Adopt a standard** | ACP as the protocol |
| Identity & secrets | **Adopt** | Keycloak + Vault (RFC 8693) |
| Memory | **Adopt** | Graphiti |
| Observability | **Adopt** | Langfuse |
| Guard-rail engine | **Adopt/check** | LLM Guard + Galileo Agent Control |
| **Scheduler + `blocked` + event correlation** | **BUILD** | covey's core — see [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) |
| **Org chart / employee model** | **BUILD** | covey's core — see [`02-agent-model.md`](02-agent-model.md) |
| **Secrets broker as a clean service** | **BUILD (thin)** | A bracket around Keycloak/Vault |
| **Backlog as a first-class object** | **BUILD** | Possibly an existing ticket system (D4) |

## Strategic conclusion

The question is no longer "build or not" but: **covey as a thin bracket *on top of* kagent or OpenHands Agent Canvas** — which give you runtime abstraction, per-agent isolation and config as code for free — and put your own energy into what nobody delivers:

1. the **scheduler with a real `blocked` state** and channel-independent event correlation (D1),
2. the **org chart / employee model** (a persistent coworker instead of an ephemeral agent),
3. the **secrets broker as a clean service** around Keycloak/Vault,
4. the **backlog** as an inspectable first-class object.

That matches the MVP guiding question in [`07-open-decisions.md`](07-open-decisions.md) exactly and would cut the build effort by months. Recommended next step: a targeted deep check of **kagent** and **Agent Canvas** as a possible foundation ("fork instead of build").

## Sources

The research is based on vendor documentation (AWS AgentCore, OpenHands, kagent, Galileo, Langfuse, Graphiti) and market overviews (as of July 2026). The market moves fast — check briefly against the current state before committing (licences and self-hosting capability change, see Daytona's switch to closed source in June 2026).
