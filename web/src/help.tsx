import type { ReactNode } from "react";

export type HelpTopic = {
  id: string;
  title: string;
  match: (path: string) => boolean;
  body: ReactNode;
};

const Badge = ({ st, children }: { st: string; children: ReactNode }) => (
  <span className={`badge st-${st}`} style={{ fontSize: 11, padding: "1px 8px" }}>
    {children}
  </span>
);

const Term = ({ children }: { children: ReactNode }) => <span className="mono text-[12px]">{children}</span>;

const deTopics: HelpTopic[] = [
  {
    id: "erste-schritte",
    title: "Erste Schritte",
    match: () => false,
    body: (
      <>
        <p>
          Covey behandelt KI-Agenten wie Mitarbeiter: jeder hat eine Identität, ein eigenes Backlog,
          eine isolierte Sandbox und bekommt Zugänge nur kurzlebig gebrokert. Der schnellste Weg zum
          ersten arbeitenden Agenten:
        </p>
        <ol>
          <li>
            Unter <b>Secrets</b> das Runtime-Credential hinterlegen: <Term>anthropic_api_key</Term>{" "}
            (API-Key) oder <Term>claude_code_oauth_token</Term> (Abo-Account, Token aus{" "}
            <Term>claude setup-token</Term>). Ohne dieses Secret schlagen Aufgaben fehl.
          </li>
          <li>
            Unter <b>Agenten</b> einen Agenten anlegen (Runtime <Term>claude-code</Term>).
          </li>
          <li>
            Auf der Agenten-Seite im Tab <b>Config</b> die <Term>SOUL.md</Term> schreiben — Rolle,
            Ton, Arbeitsweise des Agenten.
          </li>
          <li>
            Im Tab <b>Backlog</b> eine Aufgabe anlegen und den Agenten mit <b>Wecken</b> starten.
          </li>
          <li>
            Im Tab <b>Recording</b> zusehen, was passiert — jede Aktion wird aufgezeichnet.
          </li>
        </ol>
      </>
    ),
  },
  {
    id: "agenten",
    title: "Agenten (Übersicht)",
    match: (p) => p === "/",
    body: (
      <>
        <p>
          Die Übersicht zeigt alle Agenten der Organisation mit ihrem Lebenszyklus-Status. Agenten
          sind ereignisgetrieben: sie schlafen, bis Arbeit da ist, und kosten dabei nichts.
        </p>
        <dl>
          <dt><Badge st="sleeping">schläft</Badge></dt>
          <dd>Keine Sandbox aktiv. Offene Aufgaben oder „Wecken" starten den Agenten.</dd>
          <dt><Badge st="triggered">geweckt</Badge></dt>
          <dd>Sandbox fährt hoch, der Daemon verbindet sich mit der Control Plane.</dd>
          <dt><Badge st="working">arbeitet</Badge></dt>
          <dd>Die Runtime bearbeitet eine Aufgabe aus dem Backlog.</dd>
          <dt><Badge st="killed">gestoppt</Badge></dt>
          <dd>Kill-Switch aktiv — der Agent wird nicht mehr geweckt, bis er fortgesetzt wird.</dd>
        </dl>
        <p>
          <b>Notaus (Flotte)</b> stoppt alle Agenten der Organisation sofort und verhindert jedes
          Wecken, bis der Notaus gelöst wird. Sichtbar für Plattform-Admin und Security.
        </p>
      </>
    ),
  },
  {
    id: "organisation",
    title: "Organigramm",
    match: (p) => p === "/org",
    body: (
      <>
        <p>
          Der Org-Chart ist die unternehmensweite Landkarte über <b>Menschen und Agenten</b>: Agenten
          berichten an ihren Vorgesetzten (an den sie eskalieren), Menschen an ihre Führungskraft.
        </p>
        <ul>
          <li>
            <b>Zuordnen:</b> direkt im Org-Chart per Drag &amp; Drop — Menschen wie Agenten unter einen
            Vorgesetzten oder in eine Abteilung ziehen (Plattform-Admin und Agent-Owner).
          </li>
          <li>
            Agenten ohne Vorgesetzten erscheinen unterhalb des Baums, bis sie zugeordnet sind.
          </li>
        </ul>
        <p>
          Die Beziehung ist mehr als Deko: Bei einer Eskalation vermerkt Covey den Vorgesetzten im
          Ergebnis der Aufgabe.
        </p>
      </>
    ),
  },
  {
    id: "agent",
    title: "Agenten-Seite & Backlog",
    match: (p) => p.startsWith("/agents/"),
    body: (
      <>
        <p>
          Jeder Agent arbeitet sein Backlog seriell ab. Aufgaben durchlaufen Zustände:
        </p>
        <dl>
          <dt><Badge st="open">offen</Badge></dt>
          <dd>Wartet im Backlog; der nächste Lauf des Agenten greift sie auf.</dd>
          <dt><Badge st="in_progress">in Arbeit</Badge></dt>
          <dd>Die Runtime bearbeitet die Aufgabe gerade in der Sandbox.</dd>
          <dt><Badge st="blocked">wartet</Badge></dt>
          <dd>
            Der Agent braucht etwas von außen (Kundenantwort, Freigabe). Die Sandbox fährt herunter
            — es entstehen keine Kosten. Der Eintrag „wartet auf" zeigt den Korrelationsschlüssel;
            trifft das Ereignis ein, wird der Agent geweckt und setzt die Runtime-Session fort.
          </dd>
          <dt><Badge st="done">erledigt</Badge> / <Badge st="failed">fehlgeschlagen</Badge></dt>
          <dd>Abgeschlossen; Ergebnis bzw. Fehler stehen in der aufgeklappten Aufgabe.</dd>
        </dl>
        <p>Die Tabs im Überblick:</p>
        <ul>
          <li>
            <b>Backlog</b> — Aufgaben anlegen und verfolgen. „Wecken" startet die Abarbeitung sofort.
          </li>
          <li>
            <b>Heartbeat</b> — die wiederkehrenden Aufgaben aus <Term>HEARTBEAT.md</Term> grafisch:
            Zeitplan, letzter Lauf und die nächsten Läufe auf einer 24-Stunden-Zeitachse.
          </li>
          <li>
            <b>Recording</b> — lückenlose Aufzeichnung: Runtime-Events, Aktionen, Guard-Rail- und
            Credential-Entscheidungen, Lifecycle.
          </li>
          <li>
            <b>Config</b> — Config-as-Code: <Term>SOUL.md</Term> (Verhalten),{" "}
            <Term>ACCESS.md</Term> (Zugriffs-Referenzen, nie Secrets) und{" "}
            <Term>HEARTBEAT.md</Term> (wiederkehrende Aufgaben, die nach Zeitplan automatisch im
            Backlog landen). Jede Änderung erzeugt eine neue Version.
          </li>
          <li>
            <b>Gedächtnis</b> — Episoden, die der Agent aus erledigten Aufgaben gelernt hat; sie
            fließen in künftige Läufe ein.
          </li>
        </ul>
        <p>
          Die Kostenleiste zeigt LLM-Kosten und Tokens. Ist ein Budget gesetzt, pausiert der Agent
          beim Überschreiten (Budget-Deckel ist eine Guard-Rail).
        </p>
        <p>
          <b>Stoppen</b> ist der Kill-Switch für diesen Agenten: die Sandbox wird sofort beendet,
          laufende Aufgaben gehen zurück ins Backlog.
        </p>
      </>
    ),
  },
  {
    id: "freigaben",
    title: "Freigaben",
    match: (p) => p.startsWith("/approvals"),
    body: (
      <>
        <p>
          Verlangt eine Guard-Rail für eine Aktion eine Freigabe (z. B.{" "}
          <Term>zammad:reply_external</Term>), pausiert die Aufgabe als{" "}
          <Badge st="blocked">wartet</Badge> und die Anfrage erscheint hier — mit der Aktion und
          ihren Parametern.
        </p>
        <ul>
          <li><b>Freigeben</b> — der Agent wird geweckt und führt die Aktion aus.</li>
          <li><b>Ablehnen</b> — der Agent wird geweckt und muss ohne die Aktion weiterarbeiten.</li>
        </ul>
        <p>
          Entscheidungen sind Teil des Recordings und damit auditierbar. Die Zahl an „Freigaben" in
          der Navigation zeigt offene Anfragen.
        </p>
      </>
    ),
  },
  {
    id: "guardrails",
    title: "Guard-Rails",
    match: (p) => p.startsWith("/guardrails"),
    body: (
      <>
        <p>
          Guard-Rails werden zentral erzwungen — am Broker und im Tool-Layer, außerhalb der Runtime.
          Sie sind fail-closed: was keine Regel erlaubt, entscheidet der Broker, nicht der
          Agenten-Prompt. Muster erlauben Wildcards (<Term>hr*</Term>).
        </p>
        <dl>
          <dt><Badge st="failed">System verboten</Badge></dt>
          <dd>Kein Credential für dieses System — Anfragen werden am Broker abgelehnt.</dd>
          <dt><Badge st="failed">Aktion verboten</Badge></dt>
          <dd>Die Aktion wird im Tool-Layer blockiert, bevor sie das Zielsystem erreicht.</dd>
          <dt><Badge st="blocked">Freigabe-Pflicht</Badge></dt>
          <dd>Die Aktion pausiert, bis ein Mensch unter „Freigaben" entscheidet.</dd>
          <dt><Badge st="blocked">Budget-Deckel</Badge></dt>
          <dd>Überschreitet ein Agent sein Kostenlimit, pausiert er.</dd>
        </dl>
        <p>
          Eine engere Ebene kann verschärfen; eine globale Deny-Regel kann nie aufgeweicht werden.
        </p>
      </>
    ),
  },
  {
    id: "secrets",
    title: "Secrets",
    match: (p) => p.startsWith("/secrets"),
    body: (
      <>
        <p>
          Secrets liegen AES-GCM-verschlüsselt in der Datenbank. Per Default sind sie einfache
          Variablen (Servernamen, URLs) und bleiben einsehbar; als <b>sensibel</b> markierte Werte
          (Tokens, Passwörter) sind write-only — über die API nie wieder lesbar, und die Markierung
          lässt sich nicht aufheben. Der Broker reicht sie zur Laufzeit kurzlebig an Sandboxen
          durch — nie dauerhaft.
        </p>
        <p>Konventionen:</p>
        <ul>
          <li>
            <Term>anthropic_api_key</Term> — API-Key für die Claude-Code-Runtime, <i>oder</i>{" "}
            <Term>claude_code_oauth_token</Term> — OAuth-Token für Abo-Accounts (einmalig mit{" "}
            <Term>claude setup-token</Term> erzeugen). Ohne eines der beiden scheitern Aufgaben mit
            einem Credential-Fehler.
          </li>
          <li>
            <Term>&lt;system&gt;_url</Term> und <Term>&lt;system&gt;_token</Term> — Zielsysteme, z. B.{" "}
            <Term>zammad_url</Term> / <Term>zammad_token</Term>.
          </li>
        </ul>
        <p>Bearbeiten dürfen Plattform-Admin und Security.</p>
      </>
    ),
  },
  {
    id: "egress",
    title: "Egress (Netzwerk-Ausgang)",
    match: (p) => p.startsWith("/egress"),
    body: (
      <>
        <p>
          Jede Sandbox darf ausgehend nur Hosts erreichen, die auf der Allowlist ihres Agenten
          stehen — alles andere blockt der Egress-Proxy <b>fail-closed</b>. Der Arbeitsablauf:
        </p>
        <ol>
          <li>
            Unter <b>Templates</b> wiederverwendbare Host-Sets pflegen — entweder aus dem
            mitgelieferten <b>Katalog</b> übernehmen (GitHub, npm, PyPI, Container-Registries …)
            oder eigene anlegen, z. B. „Zammad-Prod" mit <Term>helpdesk.example.com</Term>.
          </li>
          <li>
            Auf der Agenten-Seite im Reiter <b>Egress</b> Templates zuweisen — dort sind auch
            agent-eigene Einzel-Hosts für Ausnahmen möglich.
          </li>
          <li>
            Die <b>Übersicht</b> zeigt Enforcement-Status, Kennzahlen der letzten 24 Stunden und
            jede einzelne Entscheidung des Proxy. Dort liegt auch die <b>Basis-Allowlist</b>:
            Hosts, die jeder Agent der Organisation erreichen darf — vorbelegt mit dem
            LLM-Endpunkt (<Term>api.anthropic.com</Term>), vollständig konfigurierbar.
          </li>
        </ol>
        <p>
          Muster: exakter Host (<Term>api.example.com</Term>) oder Wildcard für Subdomains
          (<Term>*.example.com</Term>), ohne Schema, Pfad oder Port. Änderungen greifen innerhalb
          von ~15 Sekunden (Cache des Proxy). Viele „blockiert"-Einträge bedeuten fehlende
          Allowlist-Einträge — oder einen Agenten, der wohin will, wo er nicht hingehört.
        </p>
      </>
    ),
  },
  {
    id: "rollen",
    title: "Benutzer & Rollen",
    match: (p) => p.startsWith("/users") || p.startsWith("/orgs"),
    body: (
      <>
        <p>Der Zugriff folgt Rollen (RBAC), Einheit ist die Organisation:</p>
        <dl>
          <dt><b>Plattform-Admin</b></dt>
          <dd>Alles — inklusive Benutzer- und Organisationsverwaltung.</dd>
          <dt><b>Agent-Owner</b></dt>
          <dd>Agenten anlegen, konfigurieren, wecken, stoppen; Aufgaben ins Backlog geben.</dd>
          <dt><b>Security</b></dt>
          <dd>Guard-Rails und Secrets pflegen, Agenten stoppen, Notaus für die Flotte.</dd>
          <dt><b>Auditor</b></dt>
          <dd>Lesender Zugriff, insbesondere auf Recordings und Entscheidungen.</dd>
          <dt><b>Controlling</b></dt>
          <dd>Lesender Zugriff auf Kosten und Budgets.</dd>
        </dl>
      </>
    ),
  },
];

const enTopics: HelpTopic[] = [
  {
    id: "erste-schritte",
    title: "Getting Started",
    match: () => false,
    body: (
      <>
        <p>
          Covey treats AI agents like employees: each has an identity, its own backlog, an isolated
          sandbox, and gets credentials brokered only for short lifetimes. The fastest path to your
          first working agent:
        </p>
        <ol>
          <li>
            Under <b>Secrets</b>, store the runtime credential: <Term>anthropic_api_key</Term>{" "}
            (API key) or <Term>claude_code_oauth_token</Term> (subscription account, token from{" "}
            <Term>claude setup-token</Term>). Without this secret, tasks will fail.
          </li>
          <li>
            Under <b>Agents</b>, create an agent (Runtime <Term>claude-code</Term>).
          </li>
          <li>
            On the agent page in the <b>Config</b> tab, write <Term>SOUL.md</Term> — the agent's
            role, tone, and working style.
          </li>
          <li>
            In the <b>Backlog</b> tab, create a task and start the agent with <b>Wake</b>.
          </li>
          <li>
            Watch what happens in the <b>Recording</b> tab — every action is recorded.
          </li>
        </ol>
      </>
    ),
  },
  {
    id: "agenten",
    title: "Agents (Overview)",
    match: (p) => p === "/",
    body: (
      <>
        <p>
          The overview shows all agents in the organization with their lifecycle status. Agents are
          event-driven: they sleep until there is work, costing nothing in the meantime.
        </p>
        <dl>
          <dt><Badge st="sleeping">sleeping</Badge></dt>
          <dd>No sandbox active. Open tasks or "Wake" will start the agent.</dd>
          <dt><Badge st="triggered">triggered</Badge></dt>
          <dd>Sandbox starting up, the daemon connects to the control plane.</dd>
          <dt><Badge st="working">working</Badge></dt>
          <dd>The runtime is processing a task from the backlog.</dd>
          <dt><Badge st="killed">stopped</Badge></dt>
          <dd>Kill switch active — the agent will not be woken until resumed.</dd>
        </dl>
        <p>
          <b>Kill Switch (Fleet)</b> stops all agents in the organization immediately and prevents
          any wake-up until released. Visible to Platform Admins and Security roles.
        </p>
      </>
    ),
  },
  {
    id: "organisation",
    title: "Org Chart",
    match: (p) => p === "/org",
    body: (
      <>
        <p>
          The org chart is the company-wide map of <b>people and agents</b>: agents report to their
          supervisor (who they escalate to), people report to their manager.
        </p>
        <ul>
          <li>
            <b>Assign agents:</b> on the agent page via "reports to" (Platform Admin and Agent
            Owner).
          </li>
          <li>
            <b>Assign people:</b> in user management (Platform Admin only).
          </li>
          <li>
            Agents without a supervisor appear below the tree until assigned.
          </li>
        </ul>
        <p>
          The relationship is more than decoration: on escalation, Covey records the supervisor in
          the task result.
        </p>
      </>
    ),
  },
  {
    id: "agent",
    title: "Agent Page & Backlog",
    match: (p) => p.startsWith("/agents/"),
    body: (
      <>
        <p>
          Each agent works through its backlog sequentially. Tasks pass through states:
        </p>
        <dl>
          <dt><Badge st="open">open</Badge></dt>
          <dd>Waiting in the backlog; the agent's next run will pick it up.</dd>
          <dt><Badge st="in_progress">in progress</Badge></dt>
          <dd>The runtime is currently processing this task in the sandbox.</dd>
          <dt><Badge st="blocked">waiting</Badge></dt>
          <dd>
            The agent needs something external (customer reply, approval). The sandbox shuts down —
            no costs accrue. The "waiting on" entry shows the correlation key; when the event
            arrives, the agent is woken and resumes the runtime session.
          </dd>
          <dt><Badge st="done">done</Badge> / <Badge st="failed">failed</Badge></dt>
          <dd>Completed; result or error are shown in the expanded task.</dd>
        </dl>
        <p>Overview of tabs:</p>
        <ul>
          <li>
            <b>Backlog</b> — Create and track tasks. "Wake" starts processing immediately.
          </li>
          <li>
            <b>Heartbeat</b> — Recurring tasks from <Term>HEARTBEAT.md</Term> shown graphically:
            schedule, last run, and next runs on a 24-hour timeline.
          </li>
          <li>
            <b>Recording</b> — Complete audit trail: runtime events, actions, guard-rail and
            credential decisions, lifecycle.
          </li>
          <li>
            <b>Config</b> — Config-as-Code: <Term>SOUL.md</Term> (behavior),{" "}
            <Term>ACCESS.md</Term> (access references, never secrets) and{" "}
            <Term>HEARTBEAT.md</Term> (recurring tasks automatically added to the backlog on
            schedule). Each change creates a new version.
          </li>
          <li>
            <b>Memory</b> — Episodes the agent learned from completed tasks; they feed into future
            runs.
          </li>
        </ul>
        <p>
          The cost bar shows LLM costs and tokens. If a budget is set, the agent pauses when
          exceeded (budget cap is a guard rail).
        </p>
        <p>
          <b>Stop</b> is the kill switch for this agent: the sandbox is terminated immediately,
          running tasks go back to the backlog.
        </p>
      </>
    ),
  },
  {
    id: "freigaben",
    title: "Approvals",
    match: (p) => p.startsWith("/approvals"),
    body: (
      <>
        <p>
          When a guard rail requires an approval for an action (e.g.{" "}
          <Term>zammad:reply_external</Term>), the task pauses as{" "}
          <Badge st="blocked">waiting</Badge> and the request appears here — with the action and
          its parameters.
        </p>
        <ul>
          <li><b>Approve</b> — the agent is woken and executes the action.</li>
          <li><b>Deny</b> — the agent is woken and must continue without the action.</li>
        </ul>
        <p>
          Decisions are part of the recording and thus auditable. The "Approvals" count in the
          navigation shows pending requests.
        </p>
      </>
    ),
  },
  {
    id: "guardrails",
    title: "Guard Rails",
    match: (p) => p.startsWith("/guardrails"),
    body: (
      <>
        <p>
          Guard rails are centrally enforced — at the broker and in the tool layer, outside the
          runtime. They are fail-closed: what no rule allows, the broker decides — not the agent
          prompt. Patterns allow wildcards (<Term>hr*</Term>).
        </p>
        <dl>
          <dt><Badge st="failed">System denied</Badge></dt>
          <dd>No credential for this system — requests are rejected at the broker.</dd>
          <dt><Badge st="failed">Action denied</Badge></dt>
          <dd>The action is blocked in the tool layer before it reaches the target system.</dd>
          <dt><Badge st="blocked">Approval required</Badge></dt>
          <dd>The action pauses until a person decides under "Approvals".</dd>
          <dt><Badge st="blocked">Budget cap</Badge></dt>
          <dd>If an agent exceeds its cost limit, it pauses.</dd>
        </dl>
        <p>
          A narrower scope can tighten rules; a global deny rule can never be loosened.
        </p>
      </>
    ),
  },
  {
    id: "secrets",
    title: "Secrets",
    match: (p) => p.startsWith("/secrets"),
    body: (
      <>
        <p>
          Secrets are AES-GCM encrypted in the database. By default they are plain variables
          (server names, URLs) and remain readable; values marked as <b>sensitive</b> (tokens,
          passwords) are write-only — never readable again via the API, and the mark cannot be
          removed. The broker passes them to sandboxes at runtime for short lifetimes only —
          never permanently.
        </p>
        <p>Conventions:</p>
        <ul>
          <li>
            <Term>anthropic_api_key</Term> — API key for the Claude Code runtime, <i>or</i>{" "}
            <Term>claude_code_oauth_token</Term> — OAuth token for subscription accounts (generate
            once with <Term>claude setup-token</Term>). Without one of these, tasks fail with a
            credential error.
          </li>
          <li>
            <Term>&lt;system&gt;_url</Term> and <Term>&lt;system&gt;_token</Term> — target systems,
            e.g. <Term>zammad_url</Term> / <Term>zammad_token</Term>.
          </li>
        </ul>
        <p>Platform Admins and Security roles may edit secrets.</p>
      </>
    ),
  },
  {
    id: "egress",
    title: "Egress (Network Outbound)",
    match: (p) => p.startsWith("/egress"),
    body: (
      <>
        <p>
          Each sandbox may only reach hosts that are on its agent's allowlist outbound — everything
          else is blocked by the egress proxy <b>fail-closed</b>. The workflow:
        </p>
        <ol>
          <li>
            Under <b>Templates</b>, maintain reusable host sets — either adopt from the built-in{" "}
            <b>catalog</b> (GitHub, npm, PyPI, container registries …) or create your own, e.g.
            "Zammad-Prod" with <Term>helpdesk.example.com</Term>.
          </li>
          <li>
            On the agent page in the <b>Egress</b> tab, assign templates — agent-specific single
            hosts for exceptions are also possible there.
          </li>
          <li>
            The <b>Overview</b> shows enforcement status, metrics for the last 24 hours, and every
            individual proxy decision. The <b>Base Allowlist</b> is also there: hosts every agent
            in the organization may reach — pre-populated with the LLM endpoint (
            <Term>api.anthropic.com</Term>), fully configurable.
          </li>
        </ol>
        <p>
          Patterns: exact host (<Term>api.example.com</Term>) or wildcard for subdomains (
          <Term>*.example.com</Term>), without scheme, path, or port. Changes take effect within
          ~15 seconds (proxy cache). Many "blocked" entries mean missing allowlist entries — or an
          agent trying to reach somewhere it shouldn't.
        </p>
      </>
    ),
  },
  {
    id: "rollen",
    title: "Users & Roles",
    match: (p) => p.startsWith("/users") || p.startsWith("/orgs"),
    body: (
      <>
        <p>Access follows roles (RBAC), the unit is the organization:</p>
        <dl>
          <dt><b>Platform Admin</b></dt>
          <dd>Everything — including user and organization management.</dd>
          <dt><b>Agent Owner</b></dt>
          <dd>Create, configure, wake, and stop agents; add tasks to the backlog.</dd>
          <dt><b>Security</b></dt>
          <dd>Manage guard rails and secrets, stop agents, fleet kill switch.</dd>
          <dt><b>Auditor</b></dt>
          <dd>Read-only access, especially to recordings and decisions.</dd>
          <dt><b>Controlling</b></dt>
          <dd>Read-only access to costs and budgets.</dd>
        </dl>
      </>
    ),
  },
];

export function getHelpTopics(lang: string): HelpTopic[] {
  return lang === "en" ? enTopics : deTopics;
}

export const topicForPath = (path: string): string =>
  deTopics.find((t) => t.match(path))?.id ?? "erste-schritte";
