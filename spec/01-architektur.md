# 01 — Architektur

## System-Übersicht

Das System zerfällt in zwei Ebenen: eine zentrale **Control Plane**, die den Zustand aller Agenten kennt und steuert, und eine **Data Plane** aus isolierten Sandboxen, in denen die Agenten tatsächlich arbeiten. Die Control Plane hält keinen Agenten-Code aus — sie orchestriert, brokert und beobachtet. Die eigentliche Arbeit (LLM-Calls, Tool-Nutzung, Dateioperationen) passiert ausschließlich in der Data Plane.

```
                 ┌─────────────────────────────────────────────┐
                 │               CONTROL PLANE                  │
                 │                                              │
  Admin-UI ──────┤  Scheduler / Dispatcher                     │
                 │  Agent-Registry & Org-Chart                 │
  Event-Router ──┤  Backlog-Store                              │
     ▲           │  Identitäts- & Secrets-Broker (Keycloak)    │
     │           │  Guard-Rail-/Policy-Engine (Enforcement)    │
     │           │  Observability (Recording, Alerts, Cost)    │
     │           │  Config-Sync (Git → kompilierte Runtime-Cfg)│
     │           └──────────────────┬──────────────────────────┘
     │                              │ Daemon-Protokoll (bidirektional)
     │           ┌──────────────────┴──────────────────────────┐
     │           │                DATA PLANE                    │
     │           │   ┌────────────┐  ┌────────────┐             │
  externe        │   │  Sandbox   │  │  Sandbox   │   …         │
  Systeme  ◀────▶│   │  ┌──────┐  │  │  ┌──────┐  │             │
  (Mail,         │   │  │Daemon│  │  │  │Daemon│  │             │
  Tickets,       │   │  ├──────┤  │  │  ├──────┤  │             │
  Confluence,    │   │  │Runtime│ │  │  │Runtime│ │             │
  Teams)         │   │  └──────┘  │  │  └──────┘  │             │
                 │   │ persist.   │  │ persist.   │             │
                 │   │ /home      │  │ /home      │             │
                 │   └────────────┘  └────────────┘             │
                 └─────────────────────────────────────────────┘
```

## Control Plane

Zustandsführend und immer aktiv. Verantwortlich für:

- **Scheduler / Dispatcher** — weiß, wer schläft, wer blockiert ist, was in wessen Backlog liegt und wer als Nächstes drankommt. Weckt Sandboxen bei Bedarf. Siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md).
- **Agent-Registry & Org-Chart** — Stammdaten aller Agenten, ihre Hierarchie und Vorgesetzten-Beziehungen.
- **Backlog-Store** — persistente, inspizierbare Aufgabenlisten (siehe Lifecycle-Spec).
- **Identitäts- & Secrets-Broker** — stellt Agent-Identitäten und kurzlebige, gescopte Access-Tokens aus. Siehe [`04-identitaet-secrets.md`](04-identitaet-secrets.md).
- **Guard-Rail-/Policy-Engine** — hält die zentralen, plattform-erzwungenen Guard-Rails und entscheidet jede sicherheitsrelevante Anfrage (Credential-Request, Egress, Tool-/Action-Freigabe) gegen die geltenden Policies. Siehe [`06-observability-control.md`](06-observability-control.md).
- **Observability** — nimmt Recording-Streams entgegen, wertet aus, alarmiert, deckelt Kosten. Siehe [`06-observability-control.md`](06-observability-control.md).
- **Config-Sync** — liest die Agent-Konfiguration aus Git und kompiliert daraus System-Prompt + Runtime-Config (siehe [`02-agenten-modell.md`](02-agenten-modell.md)).

Die Control Plane muss hochverfügbar sein — sie ist der Single Point of Truth. Der Zustand (Registry, Backlog, Recording-Metadaten) liegt in einer klassischen relationalen DB (PostgreSQL passt), das Knowledge-Graph-Gedächtnis separat (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)).

## Data Plane

Die Data Plane ist bewusst **dumm und ersetzbar**. Eine Sandbox ist ein isolierter Arbeitsplatz mit persistentem Home-Verzeichnis und einem laufenden Daemon. Sie hält keinen für die Plattform kritischen Zustand außer den Arbeitsdaten des jeweiligen Agenten — geht eine Sandbox verloren, wird sie aus Config + Home neu aufgebaut.

**Isolationsmodell:** persistentes Volume + ephemere Compute. Der Agent „wacht auf", mountet sein Home, arbeitet, schläft wieder ein. Die Compute-Instanz selbst ist kurzlebig; nur das Home-Volume überlebt.

> **Build-vs-Buy:** Die Sandbox-Infrastruktur wird **nicht** from scratch gebaut. Kandidaten: Firecracker/gVisor selbst betrieben (Proxmox-Erfahrung vorhanden, aber ephemere MicroVMs sind ein eigenes Biest) oder Sandbox-as-a-Service (E2B, Daytona). Der differenzierte Teil ist die Control Plane drumherum, nicht der Container-Runner. Entscheidung offen — siehe [`07-offene-entscheidungen.md`](07-offene-entscheidungen.md).

## Runtime-Abstraktion

Das zentrale Architekturprinzip. Claude Code, OpenHands, Harness & Co. haben komplett unterschiedliche Interfaces (CLI, Framework, Full-Harness). Ein direktes Wrappen jeder Runtime ertränkt die Plattform in Sonderfällen.

Stattdessen: Die Plattform managt die **Sandbox**, nicht das Framework. In der Sandbox läuft ein schlanker **Daemon**, der ein einheitliches Protokoll spricht. Der Daemon bootstrappt die konkrete Runtime — pro Runtime ein dünner **Adapter**. Solange der Adapter die Runtime starten, mit einem Task füttern und ihre Events/Outputs abgreifen kann, ist das Framework austauschbar.

```
Daemon ── Adapter(claude-code)  → `claude -p` headless, streamt stream-json-Events (siehe 12)
       ── Adapter(openhands)    → startet OpenHands-Session via API
       ── Adapter(harness)      → startet Harness-Workflow
       ── Adapter(custom)       → beliebige LLM-Schleife
```

Adapter sind bewusst dünn. Ihre einzige Aufgabe ist die Übersetzung zwischen dem plattformweiten Daemon-Protokoll und den Runtime-Spezifika.

## Daemon-Protokoll

Bidirektional (WebSocket oder gRPC-Stream) zwischen Control Plane und Daemon. Das Protokoll ist die stabile Naht des Systems — Runtimes ändern sich, das Protokoll bleibt.

### Control Plane → Daemon

| Nachricht | Zweck |
|---|---|
| `wake` | Sandbox aufwecken, Home mounten, Daemon hochfahren |
| `assign_task` | Aufgabe (mit Backlog-ID, Kontext, Priorität) in die Runtime geben |
| `inject_config` | Kompilierte Config (System-Prompt aus `SOUL.md` etc.) setzen |
| `inject_credentials` | Kurzlebiges, gescoptes Token für ein Zielsystem übergeben |
| `approve` / `deny` | Antwort auf ein zuvor gemeldetes Approval-Gate |
| `pause` / `kill` | Agenten sofort anhalten (Kill-Switch) |
| `sleep` | Aufgabe abgeschlossen/geparkt → Sandbox herunterfahren |

### Daemon → Control Plane

| Nachricht | Zweck |
|---|---|
| `ready` | Daemon oben, Runtime bootstrapped |
| `event` | Runtime-Event (LLM-Call, Tool-Call, Kommando) → Recording |
| `request_credential` | Agent braucht Zugriff auf ein Zielsystem → Broker |
| `request_approval` | Riskante Aktion wartet auf Freigabe |
| `request_org_chart` | Agent fragt das Organigramm ab (Menschen & Agenten samt Profilen, Abteilungen, Vorgesetzten-Beziehungen) → Antwort `inject_org_chart` (siehe [`09-enterprise-modell.md`](09-enterprise-modell.md)) |
| `blocked` | Agent parkt Aufgabe, wartet auf externes Ereignis (mit Korrelations-Key) |
| `task_done` | Aufgabe abgeschlossen (mit Ergebnis + Gedächtnis-Update). Status `incomplete` meldet den am Turn-Limit abgebrochenen Lauf — Übergabe-Stand statt Ergebnis (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `request_create_task` | Agent legt eine Aufgabe an: Teilaufgabe für sich selbst oder Delegation an einen Kollegen → Antwort `inject_create_task` (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `set_stage` | Agent schiebt die Aufgabe in eine (ggf. neue) Kanban-Stage — rein anzeigend, kein Lifecycle-Übergang (siehe [`03-lifecycle-scheduling.md`](03-lifecycle-scheduling.md)) |
| `note` | Proaktive Notiz des Agenten mitten im Lauf: `scope=task` hängt sie an die Aufgabe, `scope=memory` speist sie sofort ins Gedächtnis (siehe [`05-gedaechtnis.md`](05-gedaechtnis.md)) |
| `cost` | Verbrauchte Tokens/Compute für Budget-Tracking |
| `heartbeat` | Lebenszeichen |

Die Nachrichten `set_stage`, `note`, `request_org_chart` und `request_create_task` entstehen aus Meta-Aktionen des Agenten am Action-Proxy (`POST /actions/covey/set_stage`, `…/covey/add_note`, `…/covey/remember`, `…/covey/org_chart`, `…/covey/create_task`): Der Proxy behandelt das System `covey` nicht als externes Zielsystem (keine Credentials, keine Egress-Guard-Rail), sondern reicht sie als Steuersignal an die Control Plane durch.

**Eine Ausnahme davon ist `create_task`:** Sie läuft trotzdem durch die Guard-Rails (Subjekt `covey:create_task`, bei Delegation `covey:create_task:foreign`). Die übrigen Meta-Aktionen beschreiben nur, was der Agent ohnehin gerade tut; eine Aufgabe anzulegen dagegen **erzeugt** Arbeit, Kosten und — bei Delegation — Arbeit für jemand anderen. Das muss regelbar bleiben, und zwar getrennt: Eine Organisation kann einem Agenten die Zerlegung der eigenen Arbeit erlauben und die Delegation verbieten.

Alle `event`-Nachrichten fließen in die Observability-Pipeline (siehe [`06-observability-control.md`](06-observability-control.md)). **Jeder Credential-, Egress- und Approval-Request wird von der Control Plane gegen die Guard-Rails geprüft, nie vom Daemon selbst entschieden.** Der Daemon setzt zusätzlich die tool-/action-seitigen Rails lokal durch (welche Tools/Kommandos erlaubt sind), aber die verbindliche Policy-Entscheidung liegt zentral — der Daemon ist ausführendes Organ, nicht Entscheider.

## Externe Systeme

Agenten interagieren mit Ticketsystem, Confluence, Teams, Mail etc. **immer über gebrokerte, kurzlebige Credentials** — nie mit fest eingebauten Secrets. Eingehende Ereignisse aus diesen Systemen (neues Ticket, Mail-Antwort) laufen über den **Event-Router** der Control Plane und werden dort auf Agenten und ggf. geparkte Aufgaben gemappt (Event-Korrelation, siehe Lifecycle-Spec).
