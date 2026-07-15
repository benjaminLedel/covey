# 08 — Marktumfeld & Build-vs-Adopt

Ergebnis einer Marktrecherche (Stand Juli 2026) zu Open-Source- und kommerziellen Tools, die Coveys Konzeptraum ganz oder teilweise abdecken. Ziel: nicht bauen, was es reif und self-hostbar schon gibt — und die Energie auf die Naht konzentrieren, die keiner schließt.

## Kernbefund

Coveys Konzept ist **kein weißer Fleck mehr**. „Agent Control Plane" und „AI-Workforce / AI-Coworker" sind 2026 etablierte Kategorien mit ernsthaftem Wettbewerb. Die Control Plane wird industrieweit als „Kubernetes für KI-Agenten" gerahmt (nicht nur Koordination, sondern Verwaltung, Governance, Observability). Und die Analogie, die Covey trägt — Agent als aufgabenorientiert/ephemer vs. **Coworker** als persistentes Belegschaftsmitglied mit Rolle, Skills, Tools, Budget und Reporting-Hierarchie — ist praktisch überall übernommen.

Die Differenzierung liegt daher **nicht im Konzept, sondern in einer Kombination, die so keiner anbietet**: self-hosted **und** runtime-agnostisch **und** für einen technischen Betreiber (statt No-Code-Fachanwender) **und** mit echter Sandbox-als-Arbeitsplatz. Jede Vollplattform bricht an mindestens einer dieser Achsen. Zwei Open-Source-Projekte kommen dem Kern aber so nahe, dass sie eher Fundament als Konkurrenz sind (kagent, OpenHands Agent Canvas — siehe unten).

## Die nächsten Vollplattform-Treffer

| Plattform | Deckt ab | Bricht an (für Covey) |
|---|---|---|
| **AWS Bedrock AgentCore** | Runtime, Gateway, **Identity (Token-Exchange)**, Policy, Memory, Browser/Code-Interpreter (Sandbox), Observability, Evaluations; framework-agnostisch (CrewAI, LangGraph, LlamaIndex, Strands …) | AWS-Lock-in, cloud-only, kein Self-Hosting auf eigener Infra |
| **OpenHands Agent Canvas** (OSS) | self-hosted **always-on Engineering-Team**; fährt OpenHands, Claude Code, Codex, Gemini über **ACP**; Multi-Backend (lokal/VM/Docker/Cloud); Event-Trigger (Slack/GitHub/Datadog); Enterprise: Agent Control Plane, RBAC, Budget | fokussiert auf Coding-/Technik-Agenten; keine HR-/Org-Chart-/Secrets-Broker-/Backlog-Klammer |
| **kagent** (OSS, CNCF, Istio-Gründer) | Agenten als **CRDs (GitOps, PR-Review)**; BYO-Frameworks; Substrate-Runtime mit **Isolation pro Agent**; HITL **Approval-Gates + agent-initiierte Rückfragen**; Long-term-Memory; OTel | K8s-zentriert; keine Org-/Mitarbeiter-Klammer, kein Secrets-Broker-Dienst, kein Backlog-Modell |
| **IBM watsonx Orchestrate — Agentic Control Plane** (Juni 2026) | zentrales Betreiben/Governen/Skalieren über die ganze Enterprise-Umgebung | schwergewichtig, lizenzpflichtig, stack-gebunden |
| **Microsoft Agent 365** (GA Mai 2026) | Governance-Control-Plane, entdeckt/sichert Agenten überall, Shadow-AI-Discovery (Defender/Intune) | Microsoft-Stack, Governance statt Execution |
| **AI-Workforce-SaaS** (Lindy, Relevance AI, Gumloop, OpenAI Frontier, ServiceNow Autonomous Workforce, Atomicwork, Knowlee) | **Mitarbeiter-Modell** explizit: hire/onboard/Performance-Review, eigene Identität, Permissions, wachsendes Gedächtnis; Fleet-Cockpits (Kanban, geteilter Graph, Governance) | durchweg No-Code + Closed-Cloud; für technische Betreiber zu wenig Kontrolle, nicht self-hostbar |

**OpenAI Frontier** (Feb 2026) und **Knowlee** sind die konzeptuell nächsten an Coveys Mitarbeiter-Framing; beide closed. **AgentCore** ist der architektonisch nächste Match; AWS-gebunden.

## Baukasten je Layer (Open-Source-first)

Reife, self-hostbare Bausteine, aus denen Covey sich zusammensetzt statt sie zu erfinden:

### Runtimes (das, was Covey wrappt)
OpenHands (79k+ Sterne, self-hostbar), Claude Code, Codex, Gemini — uniform ansprechbar über **ACP (Agent-Client Protocol)**. ACP ist faktisch Coveys „Daemon-Protokoll/Adapter" als bereits existierender Standard. → **Adopt.**

### Sandbox / Arbeitsplatz
- **E2B** (OSS, Firecracker-microVMs, dedizierter Kernel pro Session) — stärkste Isolation; Self-Host = echtes Infra-Projekt (Nomad/Consul).
- **Beam** (gVisor+runc, AGPL) — einfacherer Betrieb; explizit auf eigenen Credits (AWS/GCP/Azure/**Hetzner**) betreibbar.
- **Northflank** (BYOC, Kata/gVisor), **OpenSandbox** (CNCF).
- ⚠️ **Daytona** hat den Produktions-Code im Juni 2026 auf closed-source umgestellt; OSS-Repo archiviert.

→ **Adopt** (nicht from scratch; siehe D2 in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md)).

### Identität & Secrets
RFC 8693 Token Exchange (mit act-Claim) ist der **Branchenkonsens** — Microsoft, Okta, AWS, Google konvergiert; Anthropic hat Workload Identity Federation im Juni 2026 GA gemacht. Fertige Bausteine: **HashiCorp Vault** (dynamische, kurzlebige Credentials), **SPIFFE/SPIRE**, **Aembit**, **Keycloak** (dein Girona-Setup). NHI-/Posture-Tools: Astrix, Oasis, Entro, Token Security.

> **Ehrliche Konsequenz:** Coveys Identity-Ansatz ist damit **korrekt, aber table stakes** — kein Alleinstellungsmerkmal mehr. Siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md). → **Adopt** (Keycloak + Vault), nicht als Differenzierung positionieren.

### Memory
**Graphiti** (Apache 2.0, temporal) führt bei zeitlicher Genauigkeit (LongMemEval 63,8 % vs. Mem0 49,0 %); self-hostbar auf Graph-DB. ⚠️ Zep Community Edition deprecated → self-host = direkt auf der Graphiti-Library. Alternativen: Mem0 (Personalisierung), Letta/MemGPT (OS-artiges Self-Editing), Cognee (Graph+Vektor aus Dokumenten). Für Coveys `SOUL.md`-Philosophie bestätigt: **Markdown-Vault + semantische Suche** ist ein valides Muster, wenn Menschen First-Class-Autoren bleiben sollen. Siehe [`05-gedaechtnis.md`](05-gedaechtnis.md). → **Adopt** (Graphiti).

### Observability & Guard-Rails
- **Langfuse** (MIT) — Tracing, LLM-as-Judge, Prompt-Mgmt; voll self-hostbar (Postgres/ClickHouse/Redis/S3).
- Guard-Rail-Libraries: **NeMo Guardrails** (Apache), **Guardrails AI** (Apache), **LLM Guard** (MIT — Prompt-Injection, PII, Toxicity).
- **Galileo Agent Control** (open-sourced) — zentrale Policy-Control-Plane mit **hot-reloadbaren** Regeln über eine ganze Agent-Flotte. Konzeptuell fast identisch mit Coveys Guard-Rail-Engine; vor Selbstbau prüfen. Siehe [`06-observability-control.md`](06-observability-control.md). → **Adopt** (Langfuse + LLM Guard; Galileo Agent Control evaluieren).

## Build-vs-Adopt — Matrix

| Covey-Layer | Empfehlung | Baustein |
|---|---|---|
| Runtimes | **Adopt** | OpenHands, Claude Code … via ACP |
| Sandbox | **Adopt** | E2B / Beam (self-host) |
| Runtime-Abstraktion / Daemon | **Adopt-Standard** | ACP als Protokoll |
| Identität & Secrets | **Adopt** | Keycloak + Vault (RFC 8693) |
| Memory | **Adopt** | Graphiti |
| Observability | **Adopt** | Langfuse |
| Guard-Rail-Engine | **Adopt/Prüfen** | LLM Guard + Galileo Agent Control |
| **Scheduler + `blocked` + Event-Korrelation** | **BUILD** | Coveys Kern — siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md) |
| **Org-Chart / Mitarbeiter-Modell** | **BUILD** | Coveys Kern — siehe [`02-agenten-modell.md`](02-agenten-modell.md) |
| **Secrets-Broker als sauberer Dienst** | **BUILD (dünn)** | Klammer um Keycloak/Vault |
| **Backlog als First-Class-Objekt** | **BUILD** | ggf. bestehendes Ticketsystem (D4) |

## Strategische Schlussfolgerung

Die Frage ist nicht mehr „bauen oder nicht", sondern: **Covey als dünne Klammer *über* kagent oder OpenHands Agent Canvas** — die dir Runtime-Abstraktion, Per-Agent-Isolation und Config-as-Code geschenkt geben — und die eigene Energie in das stecken, was keiner liefert:

1. den **Scheduler mit echtem `blocked`-Zustand** und kanalunabhängiger Event-Korrelation (D1),
2. das **Org-Chart-/Mitarbeiter-Modell** (persistenter Coworker statt ephemerer Agent),
3. den **Secrets-Broker als sauberen Dienst** um Keycloak/Vault,
4. das **Backlog** als inspizierbares First-Class-Objekt.

Das deckt sich exakt mit der MVP-Leitfrage in [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md) und würde den Bauaufwand um Monate senken. Empfohlener nächster Schritt: eine gezielte Tiefenprüfung von **kagent** und **Agent Canvas** als mögliches Fundament („forken statt bauen").

## Quellenlage

Recherche basiert auf Anbieter-Dokumentation (AWS AgentCore, OpenHands, kagent, Galileo, Langfuse, Graphiti) und Marktübersichten (Stand Juli 2026). Der Markt bewegt sich schnell — vor Festlegungen kurz gegen den aktuellen Stand prüfen (Lizenzen und Self-Host-Fähigkeit ändern sich, siehe Daytona-Closed-Source-Wechsel Juni 2026).
