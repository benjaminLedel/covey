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
            Die <b>Einrichtung</b> durchgehen (drei Karten, jede überspringbar): Engine und Zugang —
            der Wert wird geprüft, bevor er gespeichert wird, und der Arbeitsplatz entsteht gleich
            mit. Dann in drei Sätzen, was Ihr Unternehmen macht. Zuletzt die{" "}
            <b>Personalabteilung</b>: ein Agent, dessen Aufgabe es ist, die anderen zu entwerfen.
          </li>
          <li>
            Unter <b>Agenten → Neuer Agent</b> eine <b>Ausschreibung</b> schreiben: ein paar Sätze,
            was die neue Kollegin tun soll. Die Personalabteilung macht daraus einen vollständigen
            Entwurf und fragt nach, wenn etwas Wesentliches fehlt. Wer lieber selbst formuliert,
            nimmt daneben Vorlage, manuell oder Bundle-Import.
          </li>
          <li>
            Den Entwurf im Feld <b>Bewerbungen</b> durchsehen und <b>Einstellen</b> — vorher arbeitet
            er nicht. Passt er nicht, <b>Ablehnen</b>.
          </li>
          <li>
            Im Tab <b>Backlog</b> eine Aufgabe anlegen und den Agenten mit <b>Wecken</b> starten.
          </li>
          <li>
            Im Tab <b>Recording</b> zusehen, was passiert — jede Aktion wird aufgezeichnet.
          </li>
        </ol>
        <p>
          Dieselben Schritte stehen als Checkliste auf der <b>Agenten</b>-Übersicht und haken sich
          von selbst ab, sobald sie erledigt sind. Ist alles erledigt, verschwindet sie.
        </p>
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
        <p>
          Oben stehen die <b>Bewerbungen</b>: Agenten, die angelegt, aber noch nicht eingestellt
          sind. Sie werden nicht dispatcht, ihr Heartbeat läuft nicht, ihr Webhook ist nicht scharf,
          sie bekommen keine Sandbox und kosten nichts — Aufgaben dürfen trotzdem im Backlog liegen
          und starten beim Einstellen. So entstehen alle Agenten, die nicht von Hand angelegt
          wurden: aus einer Vorlage, aus einem Bundle-Import und aus einer Ausschreibung.{" "}
          <b>Einstellen</b> zeigt vorher die Zusammenfassung — Rolle, Zugänge, Vorgesetzter,
          Budget —, <b>Ablehnen</b> verwirft den Entwurf.
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
            <b>Recording</b> — lückenlose Aufzeichnung: Runtime-Events, Aktionen, Guard-Rail- und
            Credential-Entscheidungen, Lifecycle.
          </li>
          <li>
            <b>Gedächtnis</b> — das Wiki des Agenten: verlinkte Seiten, was er aus erledigten
            Aufgaben gelernt hat, und die Träume seiner Nachtläufe.
          </li>
          <li>
            <b>Arbeitsplatz</b> — das persistente Home des Agenten als Dateibaum: nachsehen, was da
            liegt, Dateien und ganze Ordner hochladen (auch per Drag & Drop) oder ändern, Auswahl
            und Ordner als ZIP herunterladen. Die üblichen Dateien werden gleich angezeigt —
            Markdown gerendert, Bilder als Bild, PDF eingebettet, Tabellen als Tabelle. Das Home
            überlebt die Sandbox, der Zugriff geht deshalb auch am schlafenden Agenten. Jede
            Änderung steht im Recording.
          </li>
          <li>
            <b>Tools &amp; Skills</b> — womit der Agent arbeitet: <b>Zielsysteme</b> (was er dort
            tun kann, im Wortlaut seines Prompts), <b>MCP-Werkzeuge</b> (welche davon er nutzen
            darf) und <b>Skills</b> (Prozeduren, die er bei Bedarf zieht).
          </li>
          <li>
            <b>Einstellungen</b> — was man einmal einrichtet: Stammdaten, <b>Heartbeat</b> (die
            wiederkehrenden Aufgaben aus <Term>HEARTBEAT.md</Term> grafisch), Webhook-Auslöser,{" "}
            <b>Config</b> (<Term>SOUL.md</Term>, <Term>ACCESS.md</Term>, <Term>HEARTBEAT.md</Term> —
            jede Änderung erzeugt eine neue Version), Egress und Secrets.
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
    id: "offene-punkte",
    title: "Offene Punkte",
    match: (p) => p.startsWith("/improvements"),
    body: (
      <>
        <p>
          Was der Betrieb an einem Kollegen gefunden hat, landet hier — und nur hier. Drei Sorten,
          eine Liste, weil alle drei denselben Menschen brauchen:
        </p>
        <dl>
          <dt><Badge st="working">Vorschlag</Badge></dt>
          <dd>
            Eine Änderung an der Konfiguration, mit Diff. Sie ist <b>nicht in Kraft</b>: erst das
            Annehmen schreibt eine neue Version — auf demselben Weg, den ein Mensch von Hand geht,
            und mit ihm als Urheber.
          </dd>
          <dt><Badge st="pending">Befund</Badge></dt>
          <dd>
            Der Auftrag passt nicht. Den kann die Plattform nicht ändern, das kann nur der Mensch,
            der ihn verantwortet — der Punkt bleibt offen, bis er abgehakt wird.
          </dd>
          <dt><Badge st="sleeping">Issue</Badge></dt>
          <dd>Die Plattform ist die Ursache. Der Bericht liegt schon im Tracker.</dd>
        </dl>
        <p>
          <b>Wer annehmen darf, hängt an den Dateien.</b> Ein Vorschlag zu <Term>SOUL.md</Term> oder{" "}
          <Term>PLAYBOOKS.md</Term> gehört dem Verwalter des Agenten. Fasst er{" "}
          <Term>ACCESS.md</Term> oder <Term>EGRESS.md</Term> an, weitet er einen Zugang — dann
          entscheidet <Term>platform_admin</Term> oder <Term>security</Term>, wie überall sonst.
        </p>
        <p>
          Ein Vorschlag ist ein Diff gegen eine Basis. Wurde dieselbe Datei zwischenzeitlich von
          Hand geändert, wird er nicht angenommen, sondern als Konflikt gezeigt — er muss neu
          geschrieben oder verworfen werden. Abgelehnte Punkte bleiben mit ihrem Grund stehen.
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
    id: "requests",
    title: "Requests (HTTP-Verkehr)",
    match: (p) => p.startsWith("/requests"),
    body: (
      <>
        <p>
          Das Request-Log zeigt, was an den Rändern der Plattform über die Leitung ging: jeder
          Aufruf, den ein Zielsystem-Plugin nach draußen stellt (<b>raus</b>), und jeder Webhook,
          den Covey empfängt (<b>rein</b>) — <b>auch die abgelehnten</b>. Genau die sind beim
          Anbinden eines Systems die interessanten: falscher Slug, nicht aktiviertes Zielsystem,
          ungültige Signatur.
        </p>
        <p>
          Das Recording auf der Agenten-Seite sagt, <i>was</i> ein Agent getan hat; hier steht,{" "}
          <i>wie</i> es über die Schnittstelle ging — Status, Dauer und die (gekappten) Bodies.
          Ein Klick auf eine Zeile öffnet das Detail.
        </p>
        <p>
          Das Log ist <b>Diagnose, kein Audit-Trail</b>: kurze Aufbewahrung (Default 72 Stunden),
          jederzeit leerbar. Zugangsdaten sind redigiert, Header werden gar nicht erst
          gespeichert. Abschaltbar mit <Term>COVEY_REQUEST_LOG=false</Term>, Bodies allein mit{" "}
          <Term>COVEY_REQUEST_LOG_BODIES=false</Term>.
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
            Walk through <b>Setup</b> (three cards, each one skippable): the engine and its
            credential — the value is checked before it is stored, and the workplace is created
            around it. Then three sentences on what your company does. Finally the{" "}
            <b>People department</b>: an agent whose job is drafting the others.
          </li>
          <li>
            Under <b>Agents → New Agent</b>, write a <b>brief</b>: a few sentences on what the new
            colleague should do. The People department turns that into a complete draft and asks
            back if something essential is missing. Prefer to write it yourself? Template, manual
            and bundle import sit right next to it.
          </li>
          <li>
            Look the draft through under <b>Applications</b> and <b>hire</b> it — until then it does
            not work. If it does not fit, <b>reject</b> it.
          </li>
          <li>
            In the <b>Backlog</b> tab, create a task and start the agent with <b>Wake</b>.
          </li>
          <li>
            Watch what happens in the <b>Recording</b> tab — every action is recorded.
          </li>
        </ol>
        <p>
          The same steps sit as a checklist on the <b>Agents</b> overview and tick themselves off as
          you go. Once everything is done, it disappears.
        </p>
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
        <p>
          At the top sit the <b>Applications</b>: agents that have been created but not hired yet.
          They are not dispatched, their heartbeat does not fire, their webhook is not live, they
          get no sandbox and cost nothing — tasks may still be queued against them and start on
          hiring. That is how every agent comes about that was not written by hand: from a template,
          from a bundle import, and from a brief. <b>Hire</b> shows the summary first — role,
          access, supervisor, budget — and <b>Reject</b> discards the draft.
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
            <b>Recording</b> — Complete audit trail: runtime events, actions, guard-rail and
            credential decisions, lifecycle.
          </li>
          <li>
            <b>Memory</b> — the agent's wiki: linked pages, what it learned from completed tasks,
            and the dreams from its night runs.
          </li>
          <li>
            <b>Workspace</b> — the agent's persistent home as a file tree: see what it has lying
            around, upload files and whole folders (drag & drop included) or edit them, download a
            selection or a folder as a ZIP. Common file types are shown right there — markdown
            rendered, images as images, PDFs embedded, tables as tables. The home outlives the
            sandbox, so this works while the agent sleeps. Every change is recorded.
          </li>
          <li>
            <b>Tools &amp; Skills</b> — what the agent works with: <b>target systems</b> (what it
            can do there, in the wording of its prompt), <b>MCP tools</b> (which of them it may
            use) and <b>skills</b> (procedures it pulls when they apply).
          </li>
          <li>
            <b>Settings</b> — what you set up once: master data, <b>heartbeat</b> (recurring tasks
            from <Term>HEARTBEAT.md</Term> shown graphically), webhook trigger, <b>config</b> (
            <Term>SOUL.md</Term>, <Term>ACCESS.md</Term>, <Term>HEARTBEAT.md</Term> — each change
            creates a new version), egress and secrets.
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
    id: "offene-punkte",
    title: "Open items",
    match: (p) => p.startsWith("/improvements"),
    body: (
      <>
        <p>
          What operations found about a colleague lands here, and nowhere else. Three kinds, one
          list, because all three need the same person:
        </p>
        <dl>
          <dt><Badge st="working">Proposal</Badge></dt>
          <dd>
            A change to the configuration, with a diff. It is <b>not in effect</b>: only accepting
            it writes a new version — on the same path a human takes by hand, and with them as the
            author.
          </dd>
          <dt><Badge st="pending">Finding</Badge></dt>
          <dd>
            The assignment is wrong. The platform cannot change that; only the person who owns it
            can — the item stays open until it is ticked off.
          </dd>
          <dt><Badge st="sleeping">Issue</Badge></dt>
          <dd>The platform is the cause. The report is already in the tracker.</dd>
        </dl>
        <p>
          <b>Who may accept depends on the files.</b> A proposal to <Term>SOUL.md</Term> or{" "}
          <Term>PLAYBOOKS.md</Term> belongs to whoever manages the agent. If it touches{" "}
          <Term>ACCESS.md</Term> or <Term>EGRESS.md</Term> it widens an access — then{" "}
          <Term>platform_admin</Term> or <Term>security</Term> decides, as everywhere else.
        </p>
        <p>
          A proposal is a diff against a base. If the same file was edited by hand in the meantime,
          it is not accepted but shown as a conflict — it has to be rewritten or discarded. Rejected
          items stay, with their reason.
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
    id: "requests",
    title: "Requests (HTTP Traffic)",
    match: (p) => p.startsWith("/requests"),
    body: (
      <>
        <p>
          The request log shows what actually went over the wire at the edges of the platform:
          every call a target-system plugin makes outbound (<b>out</b>) and every webhook Covey
          receives (<b>in</b>) — <b>including the rejected ones</b>. Those are the interesting
          ones while connecting a system: wrong slug, target system not enabled, invalid
          signature.
        </p>
        <p>
          The recording on the agent page tells you <i>what</i> an agent did; this tells you{" "}
          <i>how</i> it went across the interface — status, duration and the (truncated) bodies.
          Click a row to open the detail.
        </p>
        <p>
          The log is <b>diagnostics, not an audit trail</b>: short retention (72 hours by
          default), clearable at any time. Credentials are redacted, headers are never stored.
          Switch it off with <Term>COVEY_REQUEST_LOG=false</Term>, bodies alone with{" "}
          <Term>COVEY_REQUEST_LOG_BODIES=false</Term>.
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
