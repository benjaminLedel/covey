import { Suspense, lazy, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, NavLink, Navigate, Route, Routes, useParams } from "react-router";
import { api, inbox, post, type Agent, type Department, type Principal } from "../api";
import { BirdMark } from "../components/BirdMark";
import LangPicker from "../components/LangPicker";
import ThemeSwitch from "../components/ThemeSwitch";

/* Die Stilvorlage der angemeldeten Oberfläche. Sie hing bisher allein an der
   Konsole; seit es zwei Schalen gibt, braucht jede sie — wer über die Wurzel
   hereinkommt, lädt die Konsole gar nicht. */
import "../app.css";

const Thread = lazy(() => import("./Thread"));
const Wartet = lazy(() => import("./Wartet"));

/* Der Workspace: die Oberfläche dessen, der MIT der Belegschaft arbeitet.
 *
 * Er ist keine Seite in der Konsole, sondern eine eigene Schale — eigene
 * Kopfzeile, eigene Navigation, eigener Grund. Das ist der Punkt: Die Konsole
 * ist die Sicht dessen, der die Belegschaft BAUT (Konfiguration, Secrets,
 * Guard-Rails, Kosten), und wer nur Arbeit übergeben will, hat mit keiner
 * dieser Fragen etwas zu tun. Ein Reiter zwischen dreizehn anderen sagt das
 * Gegenteil.
 *
 * Gewechselt wird oben, und dabei wechselt die ganze Ansicht.
 */

/** Ein Agent, der Arbeit annehmen kann. Ein Bewerber ist ein Entwurf. */
const eingestellt = (a: Agent) => a.status !== "applicant";

export default function Workspace({ me, onLogout }: { me: Principal; onLogout: () => void }) {
  const { t } = useTranslation();

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

  /* Suchen. Bei vierzig Kollegen in acht Abteilungen ist Scrollen keine
     Navigation mehr. Der Schrägstrich springt ins Feld — dieselbe Taste wie
     in der Agentenliste der Konsole. */
  const [suche, setSuche] = useState("");
  const suchfeld = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const taste = (e: KeyboardEvent) => {
      const ziel = e.target as HTMLElement | null;
      const tippt = ziel && /^(INPUT|TEXTAREA)$/.test(ziel.tagName);
      if (e.key === "/" && !tippt) {
        e.preventDefault();
        suchfeld.current?.focus();
      }
    };
    window.addEventListener("keydown", taste);
    return () => window.removeEventListener("keydown", taste);
  }, []);

  const begriff = suche.trim().toLowerCase();
  const passt = (a: Agent) =>
    !begriff ||
    [a.display_name, a.job_title, a.slug].some((f) => (f ?? "").toLowerCase().includes(begriff));
  const liste = (agents.data ?? []).filter(eingestellt).filter(passt);
  const depts = abteilungen.data ?? [];

  /* Nach Abteilung gruppiert, wie eine Kanalliste. Wer keine hat, steht unten
     unter einer eigenen Überschrift — nicht oben, damit die Gliederung nicht
     mit dem Rest anfängt. */
  const gruppen = [
    ...depts
      .map((d) => ({ id: d.id, name: d.name, color: d.color, mitglieder: liste.filter((a) => a.department_id === d.id) }))
      .filter((g) => g.mitglieder.length > 0),
    {
      id: "",
      name: t("workspace.ohneAbteilung"),
      color: "",
      mitglieder: liste.filter((a) => !a.department_id || !depts.some((d) => d.id === a.department_id)),
    },
  ].filter((g) => g.mitglieder.length > 0);

  const offen = wartend.data?.pending ?? 0;

  const abmelden = async () => {
    await post("/auth/logout");
    onLogout();
  };

  return (
    <div className="ws">
      <header className="ws-top">
        <Link to="/" className="ws-marke" aria-label="covey">
          <BirdMark size={22} />
          <span>covey</span>
        </Link>

        {/* Der Schalter. Er steht in der Mitte und nicht in einem Menü:
            Er ist die wichtigste Bewegung dieser Kopfzeile. */}
        <nav className="ws-schalter" aria-label={t("workspace.schalterAria")}>
          <span className="ws-schalter-an" aria-current="page">
            {t("workspace.workspace")}
          </span>
          <Link to="/agents" className="ws-schalter-aus">
            {t("workspace.verwaltung")}
          </Link>
        </nav>

        <div className="ws-top-rechts">
          {/* Die Pillen-Fassung: in einer Kopfzeile von 56px hat die
              segmentierte mit drei Beschriftungen keinen Platz. */}
          <ThemeSwitch variant="pill" />
          <LangPicker />
          {/* Wer nur hier arbeitet, muss sich auch hier abmelden können —
              der Weg über die Konsole wäre einer durch eine Tür, die diese
              Person gar nicht benutzt. */}
          <Link to={`/people/${me.ID}`} className="ws-ich" title={me.DisplayName || me.Email}>
            {(me.DisplayName || me.Email).slice(0, 2).toUpperCase()}
          </Link>
          <button className="ws-abmelden" onClick={abmelden}>
            {t("nav.logout")}
          </button>
        </div>
      </header>

      <div className="ws-raum">
        <nav className="ws-liste" aria-label={t("workspace.kollegen")}>
          <NavLink to="/" end className={({ isActive }) => `ws-wartet ${isActive ? "on" : ""}`}>
            <span className="ws-wartet-punkt" data-offen={offen > 0} aria-hidden="true" />
            {t("workspace.wartet")}
            {offen > 0 && <span className="ws-zahl">{offen}</span>}
          </NavLink>

          <div className="ws-suche">
            <input
              ref={suchfeld}
              type="search"
              value={suche}
              onChange={(e) => setSuche(e.target.value)}
              onKeyDown={(e) => e.key === "Escape" && setSuche("")}
              placeholder={t("workspace.suche")}
              aria-label={t("workspace.suche")}
            />
          </div>

          {agents.isLoading && <p className="ws-leise">{t("common.loading")}</p>}
          {!agents.isLoading && liste.length === 0 && (
            <p className="ws-leise">{begriff ? t("workspace.nichtsGefunden") : t("chat.noAgents")}</p>
          )}

          {gruppen.map((g) => (
            <section key={g.id || "ohne"} className="ws-gruppe">
              <h2 className="ws-gruppe-kopf">
                {g.color && <span className="ws-dept-punkt" style={{ background: g.color }} aria-hidden="true" />}
                {g.name}
              </h2>
              {g.mitglieder.map((a) => (
                <NavLink
                  key={a.id}
                  to={`/w/${a.id}`}
                  className={({ isActive }) => `ws-kollege ${isActive ? "on" : ""}`}
                >
                  <span className="ws-kollege-name">
                    {a.display_name}
                    {wartetBei.has(a.id) && (
                      <span className="ws-kollege-wartet" title={t("workspace.wartet")} aria-label={t("workspace.wartet")} />
                    )}
                  </span>
                  <span className="ws-kollege-rolle">{a.job_title || a.slug}</span>
                </NavLink>
              ))}
            </section>
          ))}
        </nav>

        <main className="ws-haupt">
          <Suspense fallback={null}>
            <Routes>
              <Route path="/" element={<Wartet me={me} />} />
              <Route path="/w/:id" element={<ThreadRoute me={me} />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        </main>
      </div>
    </div>
  );
}

/* Die Route hält den Agenten fest, damit der Verlauf beim Wechsel wirklich neu
   aufgebaut wird und nicht den vorigen mit neuen Daten weiterzeigt. */
function ThreadRoute({ me }: { me: Principal }) {
  const { id = "" } = useParams();
  return <Thread key={id} agentId={id} me={me} />;
}
