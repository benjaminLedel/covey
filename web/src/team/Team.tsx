import { Suspense, lazy, useCallback, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Navigate, Route, Routes, useParams } from "react-router";
import { PEOPLE_SLUG, api, inbox, isDraft, myThreads, type Agent, type Department, type Principal, type ThreadState } from "../api";
import { canManage } from "../pages/agent/roles";
import { BirdMark } from "../components/BirdMark";
import HelpDrawer from "../components/HelpDrawer";
import ShellFoot from "../components/ShellFoot";
import Gesicht from "../components/Gesicht";
import Suche, { SucheProvider, useSucheKuerzel } from "../components/Suche";
import { NavIcon } from "../components/navicons";

/* Die Stilvorlage der angemeldeten Oberfläche. Sie hing bisher allein an der
   Konsole; seit es zwei Schalen gibt, braucht jede sie — wer über die Wurzel
   hereinkommt, lädt die Konsole gar nicht. */
import "../app.css";

const Thread = lazy(() => import("./Thread"));
const Ueberblick = lazy(() => import("./Ueberblick"));
const Notes = lazy(() => import("../pages/Notes"));

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

/** Ein Agent, der Arbeit annehmen kann. Ein Bewerber ist ein Entwurf. */
const eingestellt = (a: Agent) => a.status !== "applicant";

/** Der Zustand, den das Gesicht zeigt — dieselbe Ableitung wie im Dashboard. */
const zustandVon = (a: Agent) => (a.killed ? "killed" : a.status === "sleeping" ? "sleeping" : "working");

/* Die Abteilungsfarbe steht am Kopf der Gruppe und nicht noch einmal am
   einzelnen Kollegen: Seit jeder ein Gesicht hat, trägt das Zeichen schon
   eine Farbe, und zwei Farbträger an einem Zeichen sind einer zu viel — der
   Ring hat das Gesicht eingerahmt, statt es zu zeigen. */

export default function Team({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();
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

  const liste = (agents.data ?? []).filter(eingestellt);
  const depts = abteilungen.data ?? [];
  /* The People department, not stopped — the door. While she is still a
     draft herself (setup without an engine leaves her one), a brief to her
     would wait forever: a draft is never dispatched. Then the door leads to
     her page, where hiring is. */
  const people = liste.find((a) => a.slug === PEOPLE_SLUG && !a.killed);
  const peopleEntwurf = !!people && isDraft(people);
  const darfEinstellen = canManage(me.Role);

  /* Who has something unread stands above the departments, newest first,
     and leaves its department until it has been read (#385, as #383 in the
     app). */
  const neu = liste
    .filter((a) => ungelesen.has(a.id))
    .sort((a, b) => Date.parse(ungelesen.get(b.id)!.last_at) - Date.parse(ungelesen.get(a.id)!.last_at));
  const gelesen = liste.filter((a) => !ungelesen.has(a.id));

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
  if (neu.length > 0) gruppen.unshift({ id: "ungelesen", name: t("team.ungelesenTitel"), color: "", mitglieder: neu });

  const offen = wartend.data?.pending ?? 0;

  return (
    <SucheProvider value={sucheOeffnen}>
    <div className="flex min-h-screen">
      <aside className="sidebar tm-sidebar">
        <div className="brand">
          <BirdMark size={26} />
          covey
          {/* Auf Höhe der Wortmarke, weil die Suche zur Schale gehört und
              nicht zur Liste darunter. */}
          <button
            className="brand-suche"
            onClick={() => sucheOeffnen()}
            title={`${t("team.suche")} (⌘K)`}
            aria-label={t("team.suche")}
          >
            <NavIcon name="search" />
          </button>
        </div>


        <nav className="tm-liste" aria-label={t("team.kollegen")}>
          {/* The places above the departments: the office, and the door to a
              new colleague (#327) — the conversation with the People
              department, who drafts, or, without one, setup, where she comes
              from. Two rows of one kind: both are where one goes, not whom
              one talks to. */}
          <div className="tm-orte">
            <NavLink to="/" end className={({ isActive }) => `tm-wartet ${isActive ? "on" : ""}`}>
              <span className="tm-wartet-punkt" data-offen={offen > 0} aria-hidden="true" />
              {t("team.ueberblick")}
              {offen > 0 && <span className="tm-zahl">{offen}</span>}
            </NavLink>
            {/* The person's own notes (#342): a place, like the office — not
                somebody to talk to. */}
            <NavLink to="/team/notes" className={({ isActive }) => `tm-wartet tm-notizen ${isActive ? "on" : ""}`}>
              <span className="tm-notizen-zeichen" aria-hidden="true">
                <NavIcon name="note" />
              </span>
              {t("mobile.notizen")}
            </NavLink>
            {darfEinstellen && (
              <Link
                to={!people ? "/setup" : peopleEntwurf ? `/agents/${people.id}` : `/team/${people.id}?einstellen=1`}
                className="tm-wartet tm-einstellen"
                title={!people ? t("team.einstellenOhne") : peopleEntwurf ? `${people.display_name} — ${t("team.einstellenEntwurf")}` : people.display_name}
              >
                {/* An empty chair where the office has its dot. */}
                <span className="tm-einstellen-punkt" aria-hidden="true">+</span>
                {t("team.einstellen")}
              </Link>
            )}
            {/* Administration is a place too (#387): a row here, not a switch
                above the list — most people go there rarely. */}
            <Link to="/agents" className="tm-wartet tm-notizen">
              <span className="tm-notizen-zeichen" aria-hidden="true">
                <NavIcon name="cog" />
              </span>
              {t("team.verwaltung")}
            </Link>
          </div>

          {agents.isLoading && <p className="tm-leise">{t("common.loading")}</p>}
          {!agents.isLoading && liste.length === 0 && <p className="tm-leise">{t("chat.noAgents")}</p>}

          {gruppen.map((g) => (
            <section key={g.id || "ohne"} className="tm-gruppe">
              <h2 className="tm-gruppe-kopf">
                {g.color && <span className="tm-dept-punkt" style={{ background: g.color }} aria-hidden="true" />}
                {g.name}
              </h2>
              {g.mitglieder.map((a) => (
                <NavLink
                  key={a.id}
                  to={`/team/${a.id}`}
                  className={({ isActive }) => `tm-kollege ${isActive ? "on" : ""} ${ungelesen.has(a.id) ? "neu" : ""}`}
                >
                  {/* Das Gesicht macht den Kollegen unterscheidbar, bevor man
                      den Namen liest, und zeigt seinen Zustand. Der Ring
                      darum trägt die Farbe der Abteilung — das Einzige an
                      dieser Liste, was aus der Organisation selbst kommt. */}
                  <span className="tm-kollege-zeichen">
                    <Gesicht schluessel={a.slug} zustand={zustandVon(a)} groesse={22} />
                  </span>
                  <span className="tm-kollege-text">
                    <span className="tm-kollege-name">
                      <span className="tm-kollege-wort">{a.display_name}</span>
                      {wartetBei.has(a.id) && (
                        <span className="tm-kollege-wartet" title={t("team.wartet")} aria-label={t("team.wartet")} />
                      )}
                    </span>
                    {/* Unread: the newest line in place of the role, and a
                        count — the name in bold says it a third way, so it
                        does not rest on the badge's colour. */}
                    <span className="tm-kollege-rolle">{ungelesen.get(a.id)?.last_text || a.job_title || a.slug}</span>
                  </span>
                  {ungelesen.has(a.id) && (
                    <span className="tm-ungelesen" aria-label={t("team.ungelesen", { count: ungelesen.get(a.id)!.unread })}>
                      {Math.min(ungelesen.get(a.id)!.unread, 99)}
                    </span>
                  )}
                </NavLink>
              ))}
            </section>
          ))}
        </nav>

        <ShellFoot me={me} onLogout={onLogout} onHelp={() => setHelpOpen(true)} />
      </aside>

      <main className="tm-haupt">
        <Suspense fallback={null}>
          <Routes>
            <Route path="/" element={<Ueberblick me={me} />} />
            <Route path="/team/notes" element={<div className="tm-notizen-seite"><Notes /></div>} />
            <Route path="/team/:id" element={<ThreadRoute me={me} />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </main>

      <Suche
        offen={sucheOffen}
        onClose={() => setSucheOffen(false)}
        agents={agents.data ?? []}
        departments={depts}
        wartetBei={wartetBei}
        pfad={(a) => `/team/${a.id}`}
        fokus={sucheFokus}
        onFokus={setSucheFokus}
      />
      <HelpDrawer open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
    </SucheProvider>
  );
}

/* Die Route hält den Agenten fest, damit der Verlauf beim Wechsel wirklich neu
   aufgebaut wird und nicht den vorigen mit neuen Daten weiterzeigt. */
function ThreadRoute({ me }: { me: Principal }) {
  const { id = "" } = useParams();
  return <Thread key={id} agentId={id} me={me} />;
}
