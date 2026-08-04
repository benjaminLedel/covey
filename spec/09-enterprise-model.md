# 09 — Enterprise model (the organisation as the unit)

The load-bearing principle that separates Covey from the single-user "AI employee" apps: **Covey's unit is the organisation, not the individual user.** Covey is the platform a *company* operates to manage and govern its entire agent workforce — with many human stakeholders, central governance and a company-wide org chart. Everything — agents, roles, guard rails, budgets, audit — is **org-scoped**.

## Why org level instead of user level

The single-user tools (Lindy, Relevance AI, Frontier & co.) optimise the productivity of *one person* or a small team: "your first AI employee". Covey sits one level above — it is the **shared infrastructure a company operates**, the way it operates Active Directory, a SIEM or an HR department for the whole organisation rather than for an individual employee.

Concretely that means:

- **Agents are organisation-owned resources**, not personal assistants. They belong to the company, are assigned to departments and are governed centrally.
- **There is no "the user" but many human roles** — IT, team leads, security/compliance, audit, controlling — with different rights.
- **Governance is central and org-wide**, not configured per individual.
- **The org chart is company-wide** and covers humans *and* agents. Both carry **the same profile fields** (function, contact, platform identifiers, responsibilities as well as the org-wide configurable fields from `profile_fields`) — and agents can query the org chart themselves at runtime (meta action `covey/org_chart` at the action proxy, see [`01-architecture.md`](01-architecture.md)) to look up responsibilities and escalation paths.

This delineation is deliberately the answer to the market situation (see [`08-market.md`](08-market.md)): the "AI coworker" category exists, but the mature offerings are either single-user/cloud-only SaaS or heavyweight enterprise suites. Covey's place is the self-hostable enterprise platform for a technical operator.

## Human roles & RBAC

The platform has several human stakeholders with clearly separated rights. RBAC applies **to humans too** — least privilege is not purely an agent topic.

| Role | Responsibility | Typical rights |
|---|---|---|
| **Platform admin / IT** | Operates Covey itself: sandbox infra, runtimes, platform health | Create/delete agents, kill switch (fleet-wide), infrastructure configuration |
| **Agent owner / team lead** | Accountable for individual agents of a department | Maintain `SOUL.md` & config, prioritise the backlog, approve their agent's approval gates |
| **Security / compliance** | Sets the org-wide guard rails | Define global guard rails (**not** overridable by agent owners), policy reviews |
| **Auditor** | Checks behaviour and compliance | Read-only on recording/audit trail, export for inspections |
| **Controlling / finance** | Cost control | Cost per agent/department/cost centre, budget settings |

What matters is the **separation of powers**: whoever sets guard rails (security/compliance) is not the same role as whoever operates agents (agent owner) or whoever checks (auditor). That is exactly what makes the central guard rails from [`06-observability-control.md`](06-observability-control.md) credible — an agent owner cannot soften the org-wide limits because they do not have the role for it.

## Two identity layers

Covey separates cleanly between **human identity** and **agent identity**:

- **Humans** authenticate through the organisation's identity provider — **SSO via SAML/OIDC** (Keycloak, Entra, Okta), with a joiner-mover-leaver lifecycle. RBAC hangs off this identity.
- **Agents** have their own machine identity and get access through the secrets broker (RFC 8693). Details in [`04-identity-secrets.md`](04-identity-secrets.md).

The two layers are separate but linked: when an agent acts on a human's behalf, the delegation chain (which human authorised which agent for what) is preserved in the audit trail.

## Employee profile & team directory

Beyond a login and an RBAC role, every human has a **profile**: function (job title), phone, responsibilities (free text), their **identifiers in target systems** and the values of the **org-wide configurable extra fields**. The admin defines the latter freely under *Organisations* (e.g. location, department, Slack handle; table `profile_fields`, values as a `custom` map on the profile) — a new field is pure configuration and appears immediately in every profile editor and in the team directory. The identifiers are modelled generically as a map *system → identifier* (`identities`, e.g. `{"gitlab": "maxm", "zammad": "max@company.com"}`) — target systems are plugins without a hardcoded list, and the profiles follow the same principle: a new plugin automatically gets an input field in the UI, without a schema or code change on the profile. The profile is maintained by the admin under *Users* or by self-service under *Profile*.

The purpose is not address-book cosmetics but **handovers agent → human**: the profiles are appended at dispatch time as the section *"Team (human employees)"* to every agent's system prompt (analogous to the target-system documentation, so that profile changes take effect immediately without recompiling the agent config). An agent picks the person by their responsibility and uses exactly the stored identifier — it never guesses usernames. That way the GitLab bot, for instance, knows whom to assign an issue to for testing after a fix (the GitLab plugin's `assign` action: username → user ID → `assignee_ids`).

## Organisational structure: departments, cost centres, tenants

- **Departments / teams** — agents are assigned to organisational units. The org chart maps the real structure (team lead → their agents), and guard rails as well as budgets can be scoped **per department** (see guard-rail scope in [`06-observability-control.md`](06-observability-control.md)).
- **Cost centres** — costs (from cost tracking) are aggregated per agent, department and cost centre so that controlling can charge them cleanly.
- **Tenant model** — primarily **single-org self-hosted**: one company operates one Covey instance for itself. Multi-tenancy (several isolated organisations on one instance) is optional and a later expansion stage; if it is relevant, data and policy isolation between tenants has to be provided for in the data model from the start (decision open, see [`07-open-decisions.md`](07-open-decisions.md)).

## Governance & compliance

Enterprise means: the **audit trail has to survive an external inspection**. In regulated contexts that is the actual differentiator, not the feature list.

- **A complete, org-wide audit trail** — every agent and human action traceable, exportable, with a retention policy (builds on session recording in [`06-observability-control.md`](06-observability-control.md)).
- **Role-separated responsibility** — guard-rail owner ≠ agent owner ≠ auditor (see above).
- **EU AI Act** — agents that touch employment/HR-adjacent decisions fall under the high-risk classification (Annex III). Covey has to be able to deliver the evidence that an agent acted within its authority — which is not possible at all without a clean agent identity (see [`04-identity-secrets.md`](04-identity-secrets.md)).
- **Data residency** — self-hosting (e.g. on your own Hetzner/Proxmox infrastructure) is an advantage here over the cloud SaaS competitors.

## Delineation from single-user tools

| | Single-user "AI employee" (Lindy, Relevance, Frontier …) | Covey |
|---|---|---|
| **Unit** | User / small team | Organisation |
| **The agent belongs to** | the user | the company |
| **Governance** | per agent owner, inside the tool | central, org-wide, role-separated |
| **Human roles** | essentially one | several, with RBAC |
| **Deployment** | cloud SaaS, closed | self-hostable (your own infrastructure) |
| **Audit** | tool-internal | inspection-proof, exportable |

Covey therefore does not compete with the no-code productivity apps but occupies the gap above them: **the self-hostable, runtime-agnostic agent platform a company operates and is accountable for as a whole.**
