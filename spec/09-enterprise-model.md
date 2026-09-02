# 09 — Enterprise model (the organisation as the unit)

The load-bearing principle that separates covey from the single-user "AI employee" apps: **covey's unit is the organisation, not the individual user.** covey is the platform a *company* operates to manage and govern its entire agent workforce — with many human stakeholders, central governance and a company-wide org chart. Everything — agents, roles, guard rails, budgets, audit — is **org-scoped**.

## Why org level instead of user level

The single-user tools (Lindy, Relevance AI, Frontier & co.) optimise the productivity of *one person* or a small team: "your first AI employee". covey sits one level above — it is the **shared infrastructure a company operates**, the way it operates Active Directory, a SIEM or an HR department for the whole organisation rather than for an individual employee.

Concretely that means:

- **Agents are organisation-owned resources**, not personal assistants. They belong to the company, are assigned to departments and are governed centrally.
- **There is no "the user" but many human roles** — IT, team leads, security/compliance, audit, controlling — with different rights.
- **Governance is central and org-wide**, not configured per individual.
- **The org chart is company-wide** and covers humans *and* agents. Both carry **the same profile fields** (function, contact, platform identifiers, responsibilities as well as the org-wide configurable fields from `profile_fields`) — and agents can query the org chart themselves at runtime (meta action `covey/org_chart` at the action proxy, see [`01-architecture.md`](01-architecture.md)) to look up responsibilities and escalation paths.

This delineation is deliberately the answer to the market situation (see [`08-market.md`](08-market.md)): the "AI coworker" category exists, but the mature offerings are either single-user/cloud-only SaaS or heavyweight enterprise suites. covey's place is the self-hostable enterprise platform for a technical operator.

## Human roles & RBAC

The platform has several human stakeholders with clearly separated rights. RBAC applies **to humans too** — least privilege is not purely an agent topic.

| Role | Responsibility | Typical rights |
|---|---|---|
| **Org admin / IT** (`org_admin`) | Operates this organisation's use of covey: sandbox infra, runtimes, platform health | Create/delete agents, kill switch (fleet-wide within the organisation), infrastructure configuration |
| **Agent owner / team lead** | Accountable for individual agents of a department | Maintain `SOUL.md` & config, prioritise the backlog, approve their agent's approval gates |
| **Security / compliance** | Sets the org-wide guard rails | Define global guard rails (**not** overridable by agent owners), policy reviews |
| **Auditor** | Checks behaviour and compliance | Read-only on recording/audit trail, export for inspections |
| **Controlling / finance** | Cost control | Cost per agent/department/cost centre, budget settings |

What matters is the **separation of powers**: whoever sets guard rails (security/compliance) is not the same role as whoever operates agents (agent owner) or whoever checks (auditor). That is exactly what makes the central guard rails from [`06-observability-control.md`](06-observability-control.md) credible — an agent owner cannot soften the org-wide limits because they do not have the role for it.

### The seat carries the role

A role is not a property of a person but of their **seat** — the row that ties an account to an organisation (`humans`, with `org_id`, `account_id` and `role`, unique per pair). One account can therefore hold two seats and be `auditor` in one organisation and `agent_owner` in another; switching organisations switches the role along with everything it opens. Nothing about the login changes in the process, which is what makes the switch cheap.

### The instance level is not an org role

Above the five sits exactly one more level, and it is deliberately **not** in the table: `accounts.platform_role` (`user` | `system_admin`) belongs to the login, not to any seat. It governs the installation — the list of tenants, the switches under which registration happens, the waitlist codes.

The separation is load-bearing on an instance that carries strangers. Every organisation hands out its own `org_admin`; if the tenant administration hung off that role, the first self-registered tenant could delete all the others. `system_admin` is therefore held where no organisation can reach it, and it is handed out either by the bootstrap, by `covey system-admin add` on the server, or by another system admin. It also does **not** imply a seat: whoever operates the installation need not be a member of any of its tenants, and every route that governs the instance hangs off authentication rather than off org RBAC.

Hence the two administration surfaces, and the question that tells them apart is *how far does this reach*:

| Surface | Reach | Opened by | Holds |
|---|---|---|---|
| *Administration* | the organisation of the current seat | `org_admin` | org master data and profile fields, members & roles, usage, the org's audit trail |
| *Platform* | the installation, all tenants | `system_admin` | organisations, accounts and their instance level, system settings, waitlist codes |

Day-to-day work on the workforce — secrets, target systems, skills, templates, runtimes, guard rails, egress — sits in neither. It is not administration of an organisation but the work itself, and it stays where the work is.

## Two identity layers

covey separates cleanly between **human identity** and **agent identity**:

- **Humans** authenticate through the organisation's identity provider — **SSO via SAML/OIDC** (Keycloak, Entra, Okta), with a joiner-mover-leaver lifecycle. RBAC hangs off this identity.
- **Agents** have their own machine identity and get access through the secrets broker (RFC 8693). Details in [`04-identity-secrets.md`](04-identity-secrets.md).

The two layers are separate but linked: when an agent acts on a human's behalf, the delegation chain (which human authorised which agent for what) is preserved in the audit trail.

## Employee profile & team directory

Beyond a login and an RBAC role, every human has a **profile**: function (job title), phone, responsibilities (free text), their **identifiers in target systems** and the values of the **org-wide configurable extra fields**. The admin defines the latter freely under *Administration → Profile* (e.g. location, department, Slack handle; table `profile_fields`, values as a `custom` map on the profile) — a new field is pure configuration and appears immediately in every profile editor and in the team directory. The identifiers are modelled generically as a map *system → identifier* (`identities`, e.g. `{"gitlab": "maxm", "zammad": "max@company.com"}`) — target systems are plugins without a hardcoded list, and the profiles follow the same principle: a new plugin automatically gets an input field in the UI, without a schema or code change on the profile. The profile is maintained by the admin under *Administration → Members & roles* or by self-service under *Profile*.

The purpose is not address-book cosmetics but **handovers agent → human**: the profiles are appended at dispatch time as the section *"Team (human employees)"* to every agent's system prompt (analogous to the target-system documentation, so that profile changes take effect immediately without recompiling the agent config). An agent picks the person by their responsibility and uses exactly the stored identifier — it never guesses usernames. That way the GitLab bot, for instance, knows whom to assign an issue to for testing after a fix (the GitLab plugin's `assign` action: username → user ID → `assignee_ids`).

### The organisation's own profile

The organisation carries the same kind of master data one level up: its name, and a short **description of what this company does**. It is asked for once during setup and stays editable on the org chart and under *Administration → Profile* — the same store either way ([`20-hiring-and-setup.md`](20-hiring-and-setup.md)).

It is master data rather than a setup prompt because the same three sentences answer the same question in several places: in the config of newly drafted agents, in every hiring brief, and in the system prompt of the config copilot — which today knows the agent, its target systems and its guard rails, but not the company they belong to. Stated once, used wherever the platform would otherwise have to guess or ask again.

## Organisational structure: departments, cost centres, tenants

- **Departments / teams** — agents are assigned to organisational units. The org chart maps the real structure (team lead → their agents), and guard rails as well as budgets can be scoped **per department** (see guard-rail scope in [`06-observability-control.md`](06-observability-control.md)).
- **Cost centres** — costs (from cost tracking) are aggregated per agent, department and cost centre so that controlling can charge them cleanly.
- **Tenant model** — primarily **single-org self-hosted**: one company operates one covey instance for itself. Several organisations on one instance work: they are isolated in the data model (`org_id` everywhere, enforced by middleware rather than by each handler remembering), the login sits one level above the membership, and the instance level that administers them all is out of reach of any single tenant. What an instance carrying *strangers* still needs is the data plane — sandboxes of different tenants share one Docker network today (finding G in [`003-mandantentrennung.md`](../feature-requests/003-mandantentrennung.md)).

## Governance & compliance

Enterprise means: the **audit trail has to survive an external inspection**. In regulated contexts that is the actual differentiator, not the feature list.

- **A complete, org-wide audit trail** — every agent and human action traceable, exportable, with a retention policy (builds on session recording in [`06-observability-control.md`](06-observability-control.md)).
- **Role-separated responsibility** — guard-rail owner ≠ agent owner ≠ auditor (see above).
- **EU AI Act** — agents that touch employment/HR-adjacent decisions fall under the high-risk classification (Annex III). covey has to be able to deliver the evidence that an agent acted within its authority — which is not possible at all without a clean agent identity (see [`04-identity-secrets.md`](04-identity-secrets.md)).
- **Data residency** — self-hosting (e.g. on your own Hetzner/Proxmox infrastructure) is an advantage here over the cloud SaaS competitors.

## Delineation from single-user tools

| | Single-user "AI employee" (Lindy, Relevance, Frontier …) | covey |
|---|---|---|
| **Unit** | User / small team | Organisation |
| **The agent belongs to** | the user | the company |
| **Governance** | per agent owner, inside the tool | central, org-wide, role-separated |
| **Human roles** | essentially one | several, with RBAC |
| **Deployment** | cloud SaaS, closed | self-hostable (your own infrastructure) |
| **Audit** | tool-internal | inspection-proof, exportable |

covey therefore does not compete with the no-code productivity apps but occupies the gap above them: **the self-hostable, runtime-agnostic agent platform a company operates and is accountable for as a whole.**
