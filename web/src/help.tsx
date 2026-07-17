import type { ReactNode } from "react";

// Inline-Doku: ein Thema pro Seite, kontextsensitiv über `match` an die Route
// gebunden. Inhalte hier pflegen — der HelpDrawer rendert sie nur.
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

export const helpTopics: HelpTopic[] = [
  {
    id: "erste-schritte",
    title: "Erste Schritte",
    match: () => false, // nie kontextgebunden, steht immer oben
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
            <b>Agenten zuordnen:</b> auf der Agenten-Seite über „berichtet an" (Plattform-Admin und
            Agent-Owner).
          </li>
          <li>
            <b>Menschen zuordnen:</b> in der Benutzerverwaltung (nur Plattform-Admin).
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
            <b>Config</b> — Config-as-Code: <Term>SOUL.md</Term> (Verhalten) und{" "}
            <Term>ACCESS.md</Term> (Zugriffs-Referenzen, nie Secrets). Jede Änderung erzeugt eine
            neue Version.
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
          Secrets liegen AES-GCM-verschlüsselt in der Datenbank und sind write-only: über die API
          sind Werte nie wieder lesbar. Der Broker reicht sie zur Laufzeit kurzlebig an Sandboxen
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

export const topicForPath = (path: string): string =>
  helpTopics.find((t) => t.match(path))?.id ?? "erste-schritte";
