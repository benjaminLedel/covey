import { Suspense, lazy, useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Navigate, Route, Routes, useLocation, useParams } from "react-router";
import { PEOPLE_SLUG, api, inbox, isColleague, isDraft, myConversations, myThreads, reachableAgents, type Agent, type Department, type Principal, type ThreadState, type ConversationSummary } from "../api";
import { canManage } from "../pages/agent/roles";
import HelpDrawer from "../components/HelpDrawer";
import Rail from "../components/Rail";
import Gesicht from "../components/Gesicht";
import Suche, { SucheProvider, useSucheKuerzel } from "../components/Suche";
import { Avatar } from "../components/person";
import { NavIcon } from "../components/navicons";
import { gespraechName } from "./Gespraech";

/* Die Stilvorlage der angemeldeten Oberfläche. Sie hing bisher allein an der
   Konsole; seit es zwei Schalen gibt, braucht jede sie — wer über die Wurzel
   hereinkommt, lädt die Konsole gar nicht. */
import "../app.css";

const Thread = lazy(() => import("./Thread"));
const Gespraech = lazy(() => import("./Gespraech"));
const NeuesGespraech = lazy(() => import("./NeuesGespraech"));
const Ueberblick = lazy(() => import("./Ueberblick"));
const NotesMain = lazy(() => import("../notes/NotesPane").then((m) => ({ default: m.NotesMain })));
const NotesSidebar = lazy(() => import("../notes/NotesPane").then((m) => ({ default: m.NotesSidebar })));

/* Der Workspace: die Oberfläche dessen, der MIT der Belegschaft arbeitet.
 *
 * Er ist keine Seite in der Konsole, sondern eine eigene Schale — aber
 * derselbe Bau: eine linke Spalte mit Wortmarke, Schalter, Navigation und
 * Fuß, rechts der Inhalt. Kein Querbalken. Der erste Entwurf hatte einen, und
 * das war ein Stilbruch: zwei Grundgerüste für eine Anwendung, und wer die
 * Schale wechselte, sah die Bedienelemente von oben nach links springen.
 *
 * Was hier anders ist als in der Konsole, ist der INHALT der Spalte — dort
 * dreizehn Ziele, hier die Kollegen —, nicht ihre Form.
 */

/** How many rows "Recent" holds besides the unread ones (#747). */
const ZULETZT = 6;

/* Today the time, this week the weekday, before that the date — the same
   reading as the app's list (`_when` in mobile/lib/screens/team_space.dart). */
function wann(iso: string, lang: string): string {
  const at = new Date(iso);
  const heute = new Date();
  heute.setHours(0, 0, 0, 0);
  if (at >= heute) return at.toLocaleTimeString(lang, { hour: "2-digit", minute: "2-digit" });
  const woche = new Date(heute);
  woche.setDate(heute.getDate() - 6);
  if (at >= woche) return at.toLocaleDateString(lang, { weekday: "short" });
  return at.toLocaleDateString(lang, { day: "numeric", month: "short" });
}

function Wann({ iso, lang }: { iso: string; lang: string }) {
  return (
    <time className="tm-kollege-zeit" dateTime={iso} title={new Date(iso).toLocaleString(lang)}>
      {wann(iso, lang)}
    </time>
  );
}

/** Ein Agent, der Arbeit annehmen kann. Ein Bewerber ist ein Entwurf. */
const eingestellt = isColleague;

/** Der Zustand, den das Gesicht zeigt — dieselbe Ableitung wie im Dashboard. */
const zustandVon = (a: Agent) => (a.killed ? "killed" : a.status === "sleeping" ? "sleeping" : "working");

/* Die Abteilungsfarbe steht am Kopf der Gruppe und nicht noch einmal am
   einzelnen Kollegen: Seit jeder ein Gesicht hat, trägt das Zeichen schon
   eine Farbe, und zwei Farbträger an einem Zeichen sind einer zu viel — der
   Ring hat das Gesicht eingerahmt, statt es zu zeigen. */

export default function Team({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t, i18n } = useTranslation();
  const [helpOpen, setHelpOpen] = useState(false);

  const agents = useQuery({
    queryKey: ["agents"],
    queryFn: () => api<Agent[] | null>("/agents"),
    staleTime: 60_000,
  });
  const abteilungen = useQuery({
    queryKey: ["departments"],
    queryFn: () => api<Department[] | null>("/departments"),
    staleTime: 300_000,
  });
  /* Die offenen Punkte — für die Zahl oben UND für die Marke am einzelnen
     Kollegen. Eine Liste, zwei Verwendungen: Ohne die Marke müsste man die
     Seite „Wartet auf Sie" öffnen, um zu erfahren, WER wartet, und das ist
     die Frage, die man beim Blick auf eine Kollegenliste hat. */
  const wartend = useQuery({
    queryKey: ["inbox", "workspace-liste"],
    queryFn: () => inbox({ status: "open", limit: 100 }),
    refetchInterval: 30_000,
  });
  const wartetBei = new Set((wartend.data?.items ?? []).map((e) => e.agent_id));

  /* What the person has not read (#385), per agent. On an instance that
     does not keep it the query fails and the list simply has no badges. */
  const threads = useQuery({
    queryKey: ["threads"],
    queryFn: myThreads,
    refetchInterval: 20_000,
    retry: false,
  });
  const ungelesen = new Map<string, ThreadState>(
    (threads.data ?? []).filter((th) => th.unread > 0).map((th) => [th.agent_id, th]),
  );
  /* The conversations that are not with one agent (#440): with people, and
     groups. A direct conversation with an agent stands at the agent, below. */
  const gespraeche = useQuery({
    queryKey: ["conversations"],
    queryFn: myConversations,
    refetchInterval: 20_000,
    retry: false,
  });
  const andere = (gespraeche.data ?? []).filter((c) => c.kind === "group" || !c.members.some((m) => m.kind === "agent"));
  const [neuOffen, setNeuOffen] = useState(false);

  /* Suchen. Ein Feld links oben war ein Möbelstück — immer da, selten
     benutzt, und es nahm der Liste die Zeile, die sie zum Atmen braucht.
     Jetzt: die Lupe neben der Wortmarke, ⌘K und der Schrägstrich. */
  const [sucheOffen, setSucheOffen] = useState(false);
  /* Der markierte Kollege. Er liegt in der Schale und nicht in der
     Überblendung, weil die Überblendung nicht gerendert wird, solange sie zu
     ist — und der Verlauf sie genau so aufmacht: markiert. */
  const [sucheFokus, setSucheFokus] = useState<Agent | null>(null);
  const sucheOeffnen = useCallback((a?: Agent) => {
    setSucheFokus(a ?? null);
    setSucheOffen(true);
  }, []);
  useSucheKuerzel(() => sucheOeffnen());

  /* The doors are the agents the person reaches (#440, the organisation's
     reach), and those they already talk to — a conversation that exists
     stays readable when the reach narrows. Without an answer, all. */
  const erreichbar = useQuery({ queryKey: ["reachable-agents"], queryFn: reachableAgents, staleTime: 60_000, retry: false });
  const erreicht = erreichbar.data ? new Set(erreichbar.data.agents) : null;
  const imGespraech = new Set((threads.data ?? []).map((th) => th.agent_id));
  const liste = (agents.data ?? []).filter(eingestellt).filter((a) => !erreicht || erreicht.has(a.id) || imGespraech.has(a.id));
  const depts = abteilungen.data ?? [];
  /* The People department, not stopped — the door. While she is still a
     draft herself (setup without an engine leaves her one), a brief to her
     would wait forever: a draft is never dispatched. Then the door leads to
     her page, where hiring is. */
  const people = liste.find((a) => a.slug === PEOPLE_SLUG && !a.killed);
  const peopleEntwurf = !!people && isDraft(people);
  const darfEinstellen = canManage(me.Role);

  /* When one last wrote with whom (#747): every thread and conversation
     with its last exchange. The newest stand above the departments under
     "Recent" — everything unread among them, however old —, each with its
     last line and its time, as the app's list has it. Whoever stands there
     leaves the department until something newer pushes them out. */
  const verlauf = new Map<string, ThreadState>((threads.data ?? []).map((th) => [th.agent_id, th]));
  const lang = i18n.language;
  type Zeile = { at: number; agent?: Agent; gespraech?: ConversationSummary };
  const zeilen: Zeile[] = [
    ...andere.map((c) => ({ at: Date.parse(c.last?.created_at ?? c.last_message_at), gespraech: c })),
    ...liste.filter((a) => verlauf.has(a.id)).map((a) => ({ at: Date.parse(verlauf.get(a.id)!.last_at), agent: a })),
  ]
    .filter((z) => !Number.isNaN(z.at))
    .sort((x, y) => y.at - x.at);
  const istNeu = (z: Zeile) => (z.agent ? ungelesen.has(z.agent.id) : (z.gespraech?.unread ?? 0) > 0);
  const zuletzt = zeilen.filter((z, i) => i < ZULETZT || istNeu(z));
  const inZuletzt = new Set(zuletzt.map((z) => z.agent?.id ?? z.gespraech!.id));
  const aeltereGespraeche = andere.filter((c) => !inZuletzt.has(c.id));
  const gelesen = liste.filter((a) => !inZuletzt.has(a.id));

  /* Nach Abteilung gruppiert, wie eine Kanalliste. Wer keine hat, steht unten
     unter einer eigenen Überschrift — nicht oben, damit die Gliederung nicht
     mit dem Rest anfängt. */
  const gruppen = [
    ...depts
      .map((d) => ({ id: d.id, name: d.name, color: d.color, mitglieder: gelesen.filter((a) => a.department_id === d.id) }))
      .filter((g) => g.mitglieder.length > 0),
    {
      id: "",
      name: t("team.ohneAbteilung"),
      color: "",
      mitglieder: gelesen.filter((a) => !a.department_id || !depts.some((d) => d.id === a.department_id)),
    },
  ].filter((g) => g.mitglieder.length > 0);

  const gespraechZeile = (c: ConversationSummary) => {
    const anderer = c.members.find((m) => !m.left_at && !(m.kind === "human" && m.id === me.ID));
    const at = c.last?.created_at ?? c.last_message_at;
    return (
      <NavLink
        key={c.id}
        to={`/team/c/${c.id}`}
        className={({ isActive }) => `tm-kollege ${isActive ? "on" : ""} ${c.unread > 0 ? "neu" : ""}`}
      >
        <span className="tm-kollege-zeichen">
          {c.kind === "group" ? (
            <span className="tm-gruppe-zeichen klein" aria-hidden="true">
              <NavIcon name="chat" />
            </span>
          ) : (
            <Avatar name={anderer?.name ?? "?"} human size={22} />
          )}
        </span>
        <span className="tm-kollege-text">
          <span className="tm-kollege-name">
            <span className="tm-kollege-wort">{gespraechName(c, me.ID)}</span>
            {c.last && <Wann iso={at} lang={lang} />}
          </span>
          <span className="tm-kollege-rolle">
            {c.last
              ? `${c.kind === "group" && c.last.author_name ? c.last.author_name + ": " : ""}${c.last.text}`
              : t("conversation.emptyThread")}
          </span>
        </span>
        {c.unread > 0 && (
          <span className="tm-ungelesen" aria-label={t("team.ungelesen", { count: c.unread })}>
            {Math.min(c.unread, 99)}
          </span>
        )}
      </NavLink>
    );
  };

  /* Das Gesicht macht den Kollegen unterscheidbar, bevor man den Namen
     liest, und zeigt seinen Zustand. Under "Recent" the second line is the
     last thing said; in a department it is the role. */
  const kollegeZeile = (a: Agent, mitText: boolean) => {
    const th = verlauf.get(a.id);
    return (
      <NavLink
        key={a.id}
        to={`/team/${a.id}`}
        className={({ isActive }) => `tm-kollege ${isActive ? "on" : ""} ${ungelesen.has(a.id) ? "neu" : ""}`}
      >
        <span className="tm-kollege-zeichen">
          <Gesicht schluessel={a.slug} zustand={zustandVon(a)} groesse={22} />
        </span>
        <span className="tm-kollege-text">
          <span className="tm-kollege-name">
            <span className="tm-kollege-wort">{a.display_name}</span>
            {wartetBei.has(a.id) && (
              <span className="tm-kollege-wartet" title={t("team.wartet")} aria-label={t("team.wartet")} />
            )}
            {th && <Wann iso={th.last_at} lang={lang} />}
          </span>
          <span className="tm-kollege-rolle">{(mitText && th?.last_text) || a.job_title || a.slug}</span>
        </span>
        {ungelesen.has(a.id) && (
          <span className="tm-ungelesen" aria-label={t("team.ungelesen", { count: ungelesen.get(a.id)!.unread })}>
            {Math.min(ungelesen.get(a.id)!.unread, 99)}
          </span>
        )}
      </NavLink>
    );
  };

  const pfad = useLocation().pathname;
  const notizenOffen = pfad === "/team/notes" || pfad.startsWith("/team/notes/");
  // The office wants the full width: no list column beside it (#390).
  const buero = pfad === "/";
  const notizId = notizenOffen ? (pfad.split("/")[3] ?? null) : null;

  return (
    <SucheProvider value={sucheOeffnen}>
    <div className={`flex min-h-screen tm-drei${pfad === "/team" ? " tm-liste" : ""}${/^\/team\/(c\/)?[^/]+$/.test(pfad) && !notizenOffen ? " tm-offen" : ""}`}>
      {/* The rail (#388): where one is — team, notes, administration — as
          icons, each with its name as tooltip and for screen readers. The
          list beside it changes with the choice; the content right of it
          with the row picked there. */}
      <Rail
        me={me}
        active={notizenOffen ? "notes" : buero ? "office" : "team"}
        onLogout={onLogout}
        onHelp={() => setHelpOpen(true)}
        onSearch={() => sucheOeffnen()}
      />

      {!buero && (
      <aside className="sidebar tm-sidebar">
        {notizenOffen ? (
          <Suspense fallback={null}>
            <NotesSidebar selected={notizId} />
          </Suspense>
        ) : (
        <>
        <div className="tm-spalte-kopf">
          <h1 className="tm-spalte-titel">{t("team.workspace")}</h1>
          {/* Hiring is the column's "+" (#395), as a new note is the notes'
              — the conversation with the People department, who drafts;
              her draft page while she is one; or setup without her. */}
          {/* A new conversation (#440): with a person, a colleague, or a group. */}
          <button
            className="tm-spalte-plus tm-spalte-neu"
            onClick={() => setNeuOffen(true)}
            title={t("conversation.new")}
            aria-label={t("conversation.new")}
          >
            <NavIcon name="chat" />
          </button>
          {darfEinstellen && (
            <Link
              to={!people ? "/setup" : peopleEntwurf ? `/agents/${people.id}` : `/team/${people.id}?einstellen=1`}
              className="tm-spalte-plus"
              title={!people ? t("team.einstellenOhne") : peopleEntwurf ? `${t("team.einstellen")} — ${t("team.einstellenEntwurf")}` : t("team.einstellen")}
              aria-label={t("team.einstellen")}
            >
              +
            </Link>
          )}
        </div>
        <nav className="tm-liste" aria-label={t("team.kollegen")}>
          {/* The places above the departments: the office, and the door to a
              new colleague (#327) — the conversation with the People
              department, who drafts, or, without one, setup, where she comes
              from. Two rows of one kind: both are where one goes, not whom
              one talks to. */}

          {zuletzt.length > 0 && (
            <section className="tm-gruppe">
              <h2 className="tm-gruppe-kopf">{t("team.zuletzt")}</h2>
              {zuletzt.map((z) => (z.agent ? kollegeZeile(z.agent, true) : gespraechZeile(z.gespraech!)))}
            </section>
          )}

          {aeltereGespraeche.length > 0 && (
            <section className="tm-gruppe">
              <h2 className="tm-gruppe-kopf">{t("conversation.title")}</h2>
              {aeltereGespraeche.map(gespraechZeile)}
            </section>
          )}

          {agents.isLoading && <p className="tm-leise">{t("common.loading")}</p>}
          {!agents.isLoading && liste.length === 0 && <p className="tm-leise">{t("chat.noAgents")}</p>}

          {gruppen.map((g) => (
            <section key={g.id || "ohne"} className="tm-gruppe">
              <h2 className="tm-gruppe-kopf">
                {g.color && <span className="tm-dept-punkt" style={{ background: g.color }} aria-hidden="true" />}
                {g.name}
              </h2>
              {g.mitglieder.map((a) => kollegeZeile(a, false))}
            </section>
          ))}
        </nav>

        </>
        )}
      </aside>
      )}

      <main className="tm-haupt">
        <Suspense fallback={null}>
          <Routes>
            <Route path="/" element={<Ueberblick me={me} />} />
            <Route path="/team" element={<p className="tm-start">{t("mobile.waehlen")}</p>} />
            <Route path="/team/notes" element={<NotesMain noteId={null} />} />
            <Route path="/team/notes/:noteId" element={<NoteRoute />} />
            <Route path="/team/c/:id" element={<GespraechRoute me={me} />} />
            <Route path="/team/:id" element={<ThreadRoute me={me} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </main>

      <Suche
        offen={sucheOffen}
        onClose={() => setSucheOffen(false)}
        agents={liste}
        departments={depts}
        wartetBei={wartetBei}
        pfad={(a) => `/team/${a.id}`}
        fokus={sucheFokus}
        onFokus={setSucheFokus}
      />
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
      {neuOffen && (
        <Suspense fallback={null}>
          <NeuesGespraech me={me} onClose={() => setNeuOffen(false)} />
        </Suspense>
      )}
    </div>
    </SucheProvider>
  );
}

function NoteRoute() {
  const { noteId = "" } = useParams();
  return <NotesMain noteId={noteId} />;
}

/* Die Route hält den Agenten fest, damit der Verlauf beim Wechsel wirklich neu
   aufgebaut wird und nicht den vorigen mit neuen Daten weiterzeigt. */
function ThreadRoute({ me }: { me: Principal }) {
  const { id = "" } = useParams();
  return <Thread key={id} agentId={id} me={me} />;
}

function GespraechRoute({ me }: { me: Principal }) {
  const { id = "" } = useParams();
  return <Gespraech key={id} id={id} me={me} />;
}
