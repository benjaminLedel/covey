import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { api, post, del, ApiError, isDraft, type Agent, type AgentTemplate, type Department, type Principal } from "../api";
import { rollAgentName, slugify } from "../names";
import { fmtUSD } from "../format";
import { PhaseBadge } from "../components/PhaseBadge";
import { GuidedCreate } from "./agents/GuidedCreate";
import { Brief } from "./agents/Brief";
import { Modal, ConfirmDialog } from "../components/Modal";
import { Onboarding } from "../components/Onboarding";
import { HireDialog } from "../components/HireDialog";
import { fmtBytes } from "../format";

/* Die Belegschaft nach Abteilungen, und eine Suche darüber.
 *
 * Die Übersicht war eine Kachelwand in Anlegereihenfolge. Das trägt, solange
 * man alle kennt; ab etwa einem Dutzend ist die Frage nicht mehr „wer ist da",
 * sondern „wer im Support" und „wo ist Brunhilde". Beides beantwortet dieselbe
 * Ordnung, die das Organigramm schon hat — sie stand nur nicht in der Liste.
 *
 * Die Suche lebt in der URL (?q=…): geteilte Links und der Zurück-Knopf sollen
 * funktionieren, wie überall sonst in dieser Oberfläche auch. */

// matches sucht dort, wo jemand suchen würde: Name, Rolle, Kürzel, Zustand —
// und der Name der Abteilung, damit „support" auch die findet, die dort
// arbeiten, ohne dass es in ihrem eigenen Namen steht.
export function matches(a: Agent, deptName: string, q: string, states: string[] = []): boolean {
  if (states.length && !states.includes(stateOf(a))) return false;
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return [a.display_name, a.job_title, a.slug, a.status, deptName]
    .filter(Boolean)
    .some((v) => String(v).toLowerCase().includes(needle));
}

// stateOf fasst zusammen, was die Karte als Abzeichen zeigt. Die Plattform
// kennt fünf Zustände, aber „geweckt" und „triage" sind Sekunden auf dem Weg
// ins Arbeiten — als eigene Filter wären es Knöpfe, die fast nie etwas finden.
export function stateOf(a: Agent): "working" | "sleeping" | "killed" {
  if (a.killed) return "killed";
  if (a.status === "sleeping") return "sleeping";
  return "working";
}

// Wer läuft, steht oben. Sonst entscheidet die Anlagereihenfolge, und die ist
// für niemanden eine Auskunft.
const RANK: Record<string, number> = { working: 0, triggered: 1, triage: 2, sleeping: 3, killed: 4 };
function byBusy(a: Agent, b: Agent): number {
  const ra = a.killed ? RANK.killed : (RANK[a.status] ?? 3);
  const rb = b.killed ? RANK.killed : (RANK[b.status] ?? 3);
  if (ra !== rb) return ra - rb;
  return a.display_name.localeCompare(b.display_name);
}

export type Group = { id: string | null; name: string; color: string; agents: Agent[] };

// groupByDepartment ordnet die Belegschaft so, wie das Organigramm sie ordnet:
// Abteilungen alphabetisch, „ohne Abteilung" zuletzt — nicht weil es unwichtig
// wäre, sondern weil es kein Ort ist, an dem jemand sucht. Leere Gruppen
// entfallen: bei aktiver Suche ist eine Abteilungsüberschrift ohne Treffer
// genau die Zeile, die den Blick kostet.
export function groupByDepartment(
  agents: Agent[],
  departments: Department[],
  q: string,
  states: string[] = [],
): Group[] {
  const byId = new Map(departments.map((d) => [d.id, d]));
  const groups = new Map<string, Group>();
  const ohne: Group = { id: null, name: "", color: "", agents: [] };
  for (const a of agents) {
    const d = a.department_id ? byId.get(a.department_id) : undefined;
    if (!matches(a, d?.name ?? "", q, states)) continue;
    if (!d) {
      ohne.agents.push(a);
      continue;
    }
    const g = groups.get(d.id) ?? { id: d.id, name: d.name, color: d.color, agents: [] };
    g.agents.push(a);
    groups.set(d.id, g);
  }
  const out = [...groups.values()].sort((x, y) => x.name.localeCompare(y.name));
  if (ohne.agents.length) out.push(ohne);
  for (const g of out) g.agents.sort(byBusy);
  return out;
}

const canManage = (role: string) => role === "org_admin" || role === "agent_owner";
const canSecurity = (role: string) => role === "org_admin" || role === "security";

export default function Dashboard({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const departments = useQuery({ queryKey: ["departments"], queryFn: () => api<Department[]>("/departments") });
  const [sp, setSp] = useSearchParams();
  const q = sp.get("q") ?? "";
  const states = (sp.get("status") ?? "").split(",").filter(Boolean);
  const searchRef = useRef<HTMLInputElement>(null);
  const setParam = (key: string, v: string) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        if (v) n.set(key, v);
        else n.delete(key);
        return n;
      },
      { replace: true },
    );
  const setQ = (v: string) => setParam("q", v);
  const toggleState = (st: string) =>
    setParam("status", (states.includes(st) ? states.filter((x) => x !== st) : [...states, st]).join(","));

  // „/" springt ins Suchfeld, Esc räumt es weg — bei einer Suche, die man
  // mehrmals am Tag benutzt, ist der Griff zur Maus die Bewegung, die man
  // spart. Nicht, während jemand woanders tippt: sonst frisst die Seite das
  // Zeichen aus einem anderen Feld.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const inField = ["INPUT", "TEXTAREA", "SELECT"].includes((e.target as HTMLElement)?.tagName ?? "");
      if (e.key === "/" && !inField) {
        e.preventDefault();
        searchRef.current?.focus();
        return;
      }
      if (e.key === "Escape" && document.activeElement === searchRef.current) {
        setQ("");
        searchRef.current?.blur();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });
  const fleet = useQuery({
    queryKey: ["fleet"],
    queryFn: () => api<{ fleet_killed: boolean }>("/fleet"),
  });
  const [showCreate, setShowCreate] = useState(false);
  const [hiring, setHiring] = useState<Agent | null>(null);
  const [rejecting, setRejecting] = useState<Agent | null>(null);

  // Ablehnen heißt löschen, und das ist hier verantwortbar: der Entwurf hat nie
  // gearbeitet, es gibt keinen Lauf, keine Kosten und keine Spur, die jemand
  // später bräuchte. Die Ausschreibung, aus der er hervorging, bleibt als
  // Aufgabe stehen — dort steht auch die Begründung.
  const reject = useMutation({
    mutationFn: (id: string) => del<{ ok: boolean }>(`/agents/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      setRejecting(null);
    },
  });

  const all = agents.data ?? [];
  const depts = departments.data ?? [];
  const drafts = all.filter(isDraft).filter((a) => matches(a, "", q, states));
  const groups = groupByDepartment(all.filter((a) => !isDraft(a)), depts, q, states);
  const shown = groups.reduce((n, g) => n + g.agents.length, 0) + drafts.length;

  const fleetMut = useMutation({
    mutationFn: (kill: boolean) => post(kill ? "/fleet/kill" : "/fleet/resume"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["fleet"] });
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });

  const fleetKilled = fleet.data?.fleet_killed ?? false;

  return (
    <div>
      <div className="flex items-center gap-3 mb-4">
        <h1 className="text-[22px]">{t("dashboard.title")}</h1>
        {/* Die Suche auf Augenhöhe mit der Überschrift, mittig zwischen ihr und
            den Knöpfen: eine eigene Zeile darunter kostete Höhe für nichts. Die
            Zählung „2 in der Organisation" stand daneben und beantwortete eine
            Frage, die niemand hat — was zählt, steht an den Abteilungen. */}
        {all.length > 0 && (
          <div style={{ flex: 1, display: "flex", justifyContent: "center", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <div className="agent-search">
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden>
                <circle cx="11" cy="11" r="7" />
                <line x1="16.5" y1="16.5" x2="21" y2="21" />
              </svg>
              <input
                ref={searchRef}
                type="search"
                placeholder={t("dashboard.searchPlaceholder")}
                aria-label={t("dashboard.search")}
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
              {q && (
                <button className="btn-ghost" aria-label={t("dashboard.searchClear")} onClick={() => setQ("")}>
                  ×
                </button>
              )}
              <kbd className="secondary text-xs" title={t("dashboard.searchShortcut")}>/</kbd>
            </div>
            {/* Die Chips hinter dem Feld statt darunter: eine zweite Zeile
                schob die Belegschaft nach unten, und beides zusammen ist eine
                Aussage — wonach suche ich, und wovon. */}
            {(["working", "sleeping", "killed"] as const).map((st) => (
              <button
                key={st}
                className={`badge st-${st}`}
                aria-pressed={states.includes(st)}
                style={{
                  cursor: "pointer",
                  opacity: states.length === 0 || states.includes(st) ? 1 : 0.45,
                  outline: states.includes(st) ? "1px solid var(--text-accent)" : "none",
                }}
                onClick={() => toggleState(st)}
              >
                {t(`status.${st}`)}
              </button>
            ))}
            {(q || states.length > 0) && (
              <span className="secondary text-xs">
                {t("dashboard.countFiltered", { shown, count: all.length })}
              </span>
            )}
          </div>
        )}
        {canManage(me.Role) && (
          <button className="btn primary" onClick={() => setShowCreate(true)}>
            {t("dashboard.newAgent")}
          </button>
        )}
        {canSecurity(me.Role) && !fleetKilled && (
          <button
            className="btn"
            onClick={() => fleetMut.mutate(true)}
            title={t("dashboard.emergencyStopTitle")}
            style={{ color: "var(--error)", borderColor: "var(--border-danger, var(--border))" }}
          >
            {t("dashboard.emergencyStop")}
          </button>
        )}
      </div>

      <Onboarding me={me} />

      {/* Der Füllstand des Home-Stores. Ein Speicher, der still im Hintergrund
          wächst, ist ein Betriebsrisiko — man merkt ihn, wenn die Platte voll
          ist. Deshalb hier, und mit einer Warnung davor statt danach. */}
      <StoreLevel />

      {fleetKilled && (
        <div
          className="card mb-4"
          style={{
            borderColor: "var(--border-danger)",
            display: "flex",
            alignItems: "center",
            gap: 12,
            padding: "12px 16px",
          }}
        >
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="var(--error)" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" style={{ flexShrink: 0 }}>
            <circle cx="12" cy="12" r="9" />
            <line x1="12" y1="7" x2="12" y2="12" />
            <line x1="12" y1="15" x2="12" y2="15.5" strokeWidth="2.2" />
          </svg>
          <span style={{ color: "var(--error)", flex: 1, fontSize: 13 }}>
            {t("dashboard.fleetKilledBanner")}
          </span>
          <button className="btn sm" onClick={() => fleetMut.mutate(false)} disabled={fleetMut.isPending}>
            {t("dashboard.releaseStop")}
          </button>
        </div>
      )}

      {/* Bewerbungen zuerst und in einem eigenen Feld: ein Agent, der noch
          nicht eingestellt ist, arbeitet nicht — zwischen den anderen stünde er
          da wie ein Kollege und wäre doch keiner. Die Trennung ist deshalb
          nicht Dekoration, sondern die Aussage. */}
      {drafts.length > 0 && (
        <section className="applications mb-5">
          <div className="flex items-baseline gap-2 mb-2">
            <h2 className="text-sm" style={{ fontWeight: 600 }}>{t("dashboard.applications")}</h2>
            <span className="badge st-draft">{drafts.length}</span>
            <span className="secondary text-xs">{t("dashboard.applicationsHint")}</span>
          </div>
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(250px, 1fr))" }}>
            {drafts.map((a) => (
              <AgentCard
                key={a.id}
                agent={a}
                onHire={canManage(me.Role) ? setHiring : undefined}
                onReject={canManage(me.Role) ? setRejecting : undefined}
                labelled
              />
            ))}
          </div>
        </section>
      )}

      {drafts.length > 0 && groups.length > 0 && (
        <h2 className="text-sm mb-2" style={{ fontWeight: 600 }}>
          {t("dashboard.employed")}{" "}
          <span className="secondary text-xs" style={{ fontWeight: 400 }}>{t("dashboard.employedHint")}</span>
        </h2>
      )}

      {groups.map((g) => (
        <section key={g.id ?? "ohne"} className="mb-5">
          <div className="flex items-baseline gap-2 mb-2">
            {/* Die Abteilungsfarbe ist dieselbe wie im Organigramm — zwei
                Ansichten derselben Ordnung sollen auch gleich aussehen. */}
            {g.color && (
              <span
                aria-hidden
                style={{ width: 8, height: 8, borderRadius: 2, background: g.color, display: "inline-block" }}
              />
            )}
            <h2 className="text-sm" style={{ fontWeight: 600 }}>
              {g.id ? g.name : t("dashboard.withoutDepartment")}
            </h2>
            <span className="secondary text-xs">{g.agents.length}</span>
          </div>
          <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(250px, 1fr))" }}>
            {g.agents.map((a) => (
              <AgentCard key={a.id} agent={a} />
            ))}
          </div>
        </section>
      ))}
      {all.length === 0 && <p className="muted">{t("dashboard.noAgents")}</p>}
      {all.length > 0 && shown === 0 && <p className="muted">{t("dashboard.noMatch", { q })}</p>}

      {hiring && <HireDialog agent={hiring} onClose={() => setHiring(null)} />}
      {rejecting && (
        <ConfirmDialog
          title={t("dashboard.rejectTitle", { name: rejecting.display_name })}
          confirmLabel={t("dashboard.reject")}
          pending={reject.isPending}
          onClose={() => setRejecting(null)}
          onConfirm={() => reject.mutate(rejecting.id)}
        >
          <p className="text-sm">{t("dashboard.rejectLead")}</p>
        </ConfirmDialog>
      )}

      {showCreate && (
        <CreateAgentModal
          onClose={() => setShowCreate(false)}
          onDone={(id) => {
            setShowCreate(false);
            qc.invalidateQueries({ queryKey: ["agents"] });
          }}
        />
      )}
    </div>
  );
}

/* Die Initialen: nur Buchstaben und Ziffern.
   Sonst wird aus „QA-Agent (GitLab)" ein Kreis mit „Q(" — die Klammer ist der
   erste Buchstabe des zweiten Wortes. */
function initialsOf(name: string): string {
  return name
    .split(/[\s-]+/)
    .map((w) => w.replace(/[^\p{L}\p{N}]/gu, "")[0])
    .filter(Boolean)
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/* Eine Agentenkarte.
 *
 * Der Zustand steht rechts oben, der Name links — und der Name darf umbrechen,
 * ohne dem Zustand den Platz zu nehmen: `min-w-0` am Textblock, `shrink-0` am
 * Badge. Ohne das schob ein zweizeiliger Name das Badge in die Überschrift.
 *
 * Im Bewerbungsfeld bleibt das Badge weg (`labelled`): der Kasten heißt schon
 * „Bewerbungen", ein „Bewerbung" auf jeder Karte darin sagt nichts dazu und
 * kostet genau den Platz, an dem es klemmt. */
function AgentCard({
  agent,
  onHire,
  onReject,
  labelled = false,
}: {
  agent: Agent;
  onHire?: (a: Agent) => void;
  onReject?: (a: Agent) => void;
  labelled?: boolean;
}) {
  const { t } = useTranslation();
  const draft = isDraft(agent);
  return (
    <Link to={`/agents/${agent.id}`} className="card agent-card no-underline">
      <div className="flex items-start gap-2.5 mb-3">
        <div className="avatar shrink-0">{initialsOf(agent.display_name)}</div>
        <div className="min-w-0" style={{ flex: 1 }}>
          <div className="font-medium text-sm agent-card-name">{agent.display_name}</div>
          {/* Der Job-Titel ist das, wonach das Auge scannt — „wer im Support"
              beantwortet „Software-Entwicklerin", nicht „engineer-1". Das
              Kürzel bleibt darunter, weil es in Logs und Webhooks auftaucht. */}
          {agent.job_title && (
            <div className="secondary text-xs agent-card-name">{agent.job_title}</div>
          )}
          <div className="secondary text-xs mono agent-card-name" style={{ opacity: 0.7 }}>{agent.slug}</div>
        </div>
        {/* „Schläft" und „kommt nicht hoch" sahen gleich aus. Ein Agent, dessen
            Weckversuche scheitern, bekommt deshalb sein eigenes Abzeichen —
            und der Grund steht im Titel, statt in den Rohdaten der
            Aufzeichnung zu warten (#139). */}
        {!(draft && labelled) && (
          agent.wake_trouble && !draft && !agent.killed ? (
            <span
              className="badge shrink-0 st-wake-failed"
              title={t("status.wakeFailedWhy", {
                n: agent.wake_trouble.failures,
                err: agent.wake_trouble.error ?? "",
              })}
            >
              {t("status.wakeFailed")}
            </span>
          ) : (
            <span
              className={`badge shrink-0 ${draft ? "st-draft" : `st-${agent.killed ? "killed" : agent.status}`}`}
            >
              {draft
                ? t("dashboard.draftBadge")
                : t(`status.${agent.killed ? "killed" : agent.status}`, agent.status)}
            </span>
          )
        )}
      </div>
      <div className="secondary text-xs">
        Engine: <span className="mono">{agent.runtime}</span>
        {agent.budget_usd > 0 && <> · {t("dashboard.budget")} {fmtUSD(agent.budget_usd)}</>}
      </div>
      {/* Ein Agent, der auf die Plattform wartet, sieht auf der Übersicht sonst
          aus wie einer, der arbeitet. */}
      {agent.phase && <PhaseBadge phase={agent.phase} compact />}
      {draft && (onHire || onReject) && (
        <div className="flex gap-2 agent-card-actions">
          {onHire && (
            <button
              className="btn sm primary"
              onClick={(e) => {
                e.preventDefault(); // die Karte ist ein Link — der Knopf ist es nicht
                onHire(agent);
              }}
            >
              {t("hire.action")}
            </button>
          )}
          {onReject && (
            <button
              className="btn sm"
              onClick={(e) => {
                e.preventDefault();
                onReject(agent);
              }}
            >
              {t("dashboard.reject")}
            </button>
          )}
        </div>
      )}
    </Link>
  );
}

// ---------------------------------------------------------------------------
// Anlege-Modal mit vier Pfaden: Ausschreibung · Vorlage · Manuell · Import
//
// Die Ausschreibung steht vorn und ist der Vorgabeweg: sie stellt die eine
// Frage, die jemand beantworten kann, ohne die Plattform zu kennen. Der
// manuelle Weg bleibt vollständig daneben — als Weg für den, der genau weiß,
// was er will, und als Rückfalltür, wenn die Personalabteilung nicht arbeiten
// kann (spec/20).
// ---------------------------------------------------------------------------

type CreatePath = "choose" | "brief" | "template" | "manual" | "import";

function CreateAgentModal({ onClose, onDone }: { onClose: () => void; onDone: (id: string) => void }) {
  const { t } = useTranslation();
  const [path, setPath] = useState<CreatePath>("choose");
  const navigate = useNavigate();

  const handleDone = (agent: Agent) => {
    onDone(agent.id);
    navigate(`/agents/${agent.id}`);
  };

  const titles: Record<CreatePath, string> = {
    choose: t("dashboard.createAgent"),
    brief: t("dashboard.pathBrief"),
    template: t("dashboard.fromTemplate"),
    manual: t("dashboard.manualCreate"),
    import: t("dashboard.importBundle"),
  };

  return (
    <Modal title={titles[path]} onClose={onClose} size={path === "template" ? "lg" : "md"}>
      {path === "choose" && <ChoosePath onPick={setPath} />}
      {path === "brief" && (
        <Brief onBack={() => setPath("choose")} onOpen={handleDone} />
      )}
      {path === "template" && (
        <TemplateStep onBack={() => setPath("choose")} onDone={handleDone} />
      )}
      {path === "manual" && (
        <GuidedCreate onBack={() => setPath("choose")} onDone={handleDone} />
      )}
      {path === "import" && (
        <ImportStep onBack={() => setPath("choose")} onDone={handleDone} />
      )}
    </Modal>
  );
}

const pathIcons: Record<string, React.JSX.Element> = {
  brief: (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-9" />
      <path d="M14 3v6h6" />
      <path d="M9 13h5M9 17h3" />
    </svg>
  ),
  template: (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="9" y="9" width="12" height="13" rx="2" />
      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
      <path d="M13 13h4M13 17h4" />
    </svg>
  ),
  manual: (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <rect x="5" y="8" width="14" height="11" rx="2" />
      <path d="M12 4v4" />
      <circle cx="12" cy="3.5" r="1" />
      <circle cx="9.5" cy="13" r="1" />
      <circle cx="14.5" cy="13" r="1" />
    </svg>
  ),
  import: (
    <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
  ),
};

function ChoosePath({ onPick }: { onPick: (p: CreatePath) => void }) {
  const { t } = useTranslation();
  const paths: { key: CreatePath; title: string; desc: string }[] = [
    {
      key: "brief",
      title: t("dashboard.pathBrief"),
      desc: t("dashboard.pathBriefDesc"),
    },
    {
      key: "template",
      title: t("dashboard.pathTemplate"),
      desc: t("dashboard.pathTemplateDesc"),
    },
    {
      key: "manual",
      title: t("dashboard.pathManual"),
      desc: t("dashboard.pathManualDesc"),
    },
    {
      key: "import",
      title: t("dashboard.pathImport"),
      desc: t("dashboard.pathImportDesc"),
    },
  ];

  return (
    <div style={{ display: "grid", gap: 10 }}>
      {paths.map((p) => (
        <button
          key={p.key}
          onClick={() => onPick(p.key)}
          style={{
            display: "flex",
            alignItems: "center",
            gap: 16,
            padding: "14px 16px",
            border: "1.5px solid var(--border)",
            borderRadius: 8,
            background: "var(--surface)",
            cursor: "pointer",
            textAlign: "left",
            width: "100%",
            transition: "border-color 0.15s",
          }}
          onMouseEnter={(e) => ((e.currentTarget as HTMLElement).style.borderColor = "var(--clay)")}
          onMouseLeave={(e) => ((e.currentTarget as HTMLElement).style.borderColor = "var(--border)")}
        >
          <span style={{ color: "var(--text-secondary)", flexShrink: 0 }}>{pathIcons[p.key]}</span>
          <div>
            <div style={{ fontWeight: 500, fontSize: 14, color: "var(--text-primary)" }}>{p.title}</div>
            <div className="muted text-xs" style={{ marginTop: 2 }}>{p.desc}</div>
          </div>
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" style={{ marginLeft: "auto", color: "var(--text-secondary)", flexShrink: 0 }}>
            <path d="M9 6l6 6-6 6" />
          </svg>
        </button>
      ))}
    </div>
  );
}

function TemplateStep({ onBack, onDone }: { onBack: () => void; onDone: (a: Agent) => void }) {
  const { t, i18n } = useTranslation();
  const [selected, setSelected] = useState<AgentTemplate | null>(null);
  const [displayName, setDisplayName] = useState("");
  const [slug, setSlug] = useState("");

  const templates = useQuery({
    queryKey: ["templates", i18n.language],
    queryFn: () => api<AgentTemplate[]>(`/templates?lang=${encodeURIComponent(i18n.language)}`),
  });

  const mut = useMutation({
    mutationFn: () =>
      post<{ agent: Agent; warnings: string[] }>(`/templates/${selected!.id}/instantiate`, {
        slug: slug.trim(),
        display_name: displayName.trim(),
      }),
    onSuccess: (res) => onDone(res.agent),
  });

  const list = templates.data ?? [];

  if (!selected) {
    return (
      <div>
        <BackLink onBack={onBack} />
        {templates.isLoading && <p className="muted text-sm">{t("dashboard.loadingTemplates")}</p>}
        {!templates.isLoading && list.length === 0 && (
          <div style={{ padding: "32px 0", textAlign: "center" }}>
            <p className="muted text-sm" style={{ marginBottom: 4 }}>{t("dashboard.noTemplates")}</p>
            <p className="muted text-xs">{t("dashboard.noTemplatesHint")}</p>
          </div>
        )}
        <div style={{ display: "grid", gap: 8, marginTop: 8, maxHeight: 380, overflowY: "auto" }}>
          {list.map((tpl) => {
            const bundle = tpl.bundle as { agent?: { runtime?: string } };
            return (
              <button
                key={tpl.id}
                onClick={() => {
                  setSelected(tpl);
                  setDisplayName(tpl.name);
                  setSlug(slugify(tpl.name));
                }}
                style={{
                  display: "flex",
                  alignItems: "center",
                  gap: 14,
                  padding: "12px 14px",
                  border: "1.5px solid var(--border)",
                  borderRadius: 7,
                  background: "var(--surface)",
                  cursor: "pointer",
                  textAlign: "left",
                  width: "100%",
                }}
                onMouseEnter={(e) => ((e.currentTarget as HTMLElement).style.borderColor = "var(--clay)")}
                onMouseLeave={(e) => ((e.currentTarget as HTMLElement).style.borderColor = "var(--border)")}
              >
                <div style={{ flex: 1 }}>
                  <div style={{ fontWeight: 500, fontSize: 14 }}>{tpl.name}</div>
                  {tpl.description && <div className="muted text-xs" style={{ marginTop: 2 }}>{tpl.description}</div>}
                  <div className="muted text-xs" style={{ marginTop: 4 }}>
                    Engine: <span className="mono">{bundle?.agent?.runtime ?? "—"}</span>
                  </div>
                </div>
                <span className="muted" style={{ fontSize: 18 }}>›</span>
              </button>
            );
          })}
        </div>
      </div>
    );
  }

  return (
    <div>
      <BackLink onBack={() => setSelected(null)} label={`← ${selected.name}`} />
      <form
        onSubmit={(e) => { e.preventDefault(); mut.mutate(); }}
        style={{ display: "flex", flexDirection: "column", gap: 12, marginTop: 8 }}
      >
        <div>
          <label>{t("dashboard.displayName")}</label>
          <div className="flex gap-2">
            <input
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              autoFocus
              required
              style={{ flex: 1 }}
            />
            <button
              type="button"
              className="btn"
              title={t("dashboard.rollDice")}
              onClick={async () => {
                const g = await rollAgentName();
                setDisplayName(g.name);
                setSlug(g.slug);
              }}
            >
              🎲
            </button>
          </div>
        </div>
        <div>
          <label>{t("dashboard.slug")}</label>
          <input
            value={slug}
            onChange={(e) => setSlug(e.target.value)}
            className="mono"
            required
          />
          <div className="muted text-xs" style={{ marginTop: 3 }}>{t("dashboard.slugHint")}</div>
        </div>
        {mut.isError && (
          <div className="danger-text text-xs">{String((mut.error as Error)?.message ?? mut.error)}</div>
        )}
        <div className="flex gap-2 justify-end" style={{ marginTop: 4 }}>
          <button type="button" className="btn" onClick={() => setSelected(null)}>{t("dashboard.back")}</button>
          <button type="submit" className="btn primary" disabled={mut.isPending}>
            {mut.isPending ? t("dashboard.creating") : t("dashboard.createAgentBtn")}
          </button>
        </div>
      </form>
    </div>
  );
}

function ImportStep({ onBack, onDone }: { onBack: () => void; onDone: (a: Agent) => void }) {
  const { t } = useTranslation();
  const fileRef = useRef<HTMLInputElement>(null);
  const [bundle, setBundle] = useState<{ agent?: { slug?: string } } | null>(null);
  const [fileName, setFileName] = useState("");
  const [slugOverride, setSlugOverride] = useState("");
  const [conflict, setConflict] = useState(false);
  const [parseError, setParseError] = useState("");
  const [warnings, setWarnings] = useState<string[]>([]);

  const mut = useMutation({
    mutationFn: (args: { bundle: unknown; slug?: string }) =>
      post<{ agent: Agent; warnings: string[] }>(
        `/agents/import${args.slug ? `?slug=${encodeURIComponent(args.slug)}` : ""}`,
        args.bundle,
      ),
    onSuccess: (res) => {
      setWarnings(res.warnings ?? []);
      setConflict(false);
      setParseError("");
      if (res.warnings.length === 0) {
        onDone(res.agent);
      }
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409) {
        setConflict(true);
        setParseError((err as Error).message);
      } else {
        setConflict(false);
        setParseError(String(err instanceof ApiError ? err.message : err));
      }
    },
  });

  const pick = async (f: File | undefined) => {
    if (!f) return;
    setConflict(false);
    setParseError("");
    setSlugOverride("");
    setWarnings([]);
    setFileName(f.name);
    try {
      const parsed = JSON.parse(await f.text());
      setBundle(parsed);
      mut.mutate({ bundle: parsed });
    } catch {
      setBundle(null);
      setParseError(t("dashboard.invalidJson"));
    }
  };

  const importResult = mut.isSuccess ? mut.data : null;

  return (
    <div>
      <BackLink onBack={onBack} />
      <div style={{ marginTop: 8 }}>
        <button
          className="btn"
          type="button"
          onClick={() => fileRef.current?.click()}
          disabled={mut.isPending}
          style={{ marginBottom: 8 }}
        >
          {fileName ? t("dashboard.changeFile") : t("dashboard.selectJson")}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          style={{ display: "none" }}
          onChange={(e) => { pick(e.target.files?.[0]); e.target.value = ""; }}
        />
        {fileName && <span className="muted text-xs mono" style={{ marginLeft: 8 }}>{fileName}</span>}
        {mut.isPending && <p className="muted text-xs" style={{ marginTop: 6 }}>{t("dashboard.importing")}</p>}

        {conflict && bundle && (
          <form
            className="flex gap-2 items-end mt-3 flex-wrap"
            onSubmit={(e) => { e.preventDefault(); if (slugOverride) mut.mutate({ bundle, slug: slugOverride }); }}
          >
            <div style={{ flex: 1 }}>
              <label>{t("dashboard.newSlug")}</label>
              <input
                value={slugOverride}
                onChange={(e) => setSlugOverride(e.target.value)}
                placeholder={`${bundle.agent?.slug ?? "agent"}-2`}
                className="mono"
                required
                autoFocus
              />
            </div>
            <button className="btn primary" disabled={mut.isPending || !slugOverride}>
              {t("dashboard.reimport")}
            </button>
          </form>
        )}

        {parseError && !conflict && (
          <p className="danger-text text-xs" style={{ marginTop: 8 }}>{parseError}</p>
        )}
        {conflict && (
          <p className="danger-text text-xs" style={{ marginTop: 8 }}>{parseError}</p>
        )}

        {importResult && warnings.length > 0 && (
          <div style={{ marginTop: 12 }}>
            <p className="text-sm" style={{ marginBottom: 6 }}>
              {t("dashboard.importedWith", { name: importResult.agent.display_name })}
            </p>
            <ul style={{ fontSize: 12, color: "var(--text-warning, #b58900)", paddingLeft: "1.4em", marginBottom: 12 }}>
              {warnings.map((w, i) => <li key={i}>{w}</li>)}
            </ul>
            <button className="btn primary" onClick={() => onDone(importResult.agent)}>
              {t("dashboard.openAgent")}
            </button>
          </div>
        )}
      </div>
    </div>
  );
}

function BackLink({ onBack, label }: { onBack: () => void; label?: string }) {
  const { t } = useTranslation();
  return (
    <button
      type="button"
      onClick={onBack}
      style={{
        background: "none",
        border: "none",
        padding: 0,
        cursor: "pointer",
        fontSize: 13,
        color: "var(--text-secondary)",
        marginBottom: 12,
      }}
    >
      {label ?? `← ${t("dashboard.back")}`}
    </button>
  );
}

// StoreLevel wird laut, bevor der Platz knapp wird — und schweigt sonst. Eine
// Zeile, die bei jedem Besuch dasteht und nie etwas verlangt, ist Möbelstück:
// nach zwei Wochen liest man sie nicht mehr, und die Warnung daneben auch nicht.
// Deshalb ist der Füllstand ohne Anlass unter Administration → Runner zu Hause,
// dort, wo der Aufräumen-Knopf steht.
//
// Das Kriterium ist bewusst keine Prozentzahl. "90 % voll" sind auf 2 TB noch
// 200 GB und auf 40 GB noch vier — die Zahl sagt nichts darüber, ob es reicht.
// Die ehrliche Frage ist, ob der nächste Sync landen kann, und das größte Home
// ist die beste Annäherung daran.
function StoreLevel() {
  const { t } = useTranslation();
  const store = useQuery({
    queryKey: ["home-store"],
    queryFn: () =>
      api<{
        enabled: boolean;
        bytes: number;
        agents: number;
        largest_home_bytes: number;
        total_bytes: number;
        free_bytes: number;
      }>("/platform/home-store"),
  });
  const d = store.data;
  // Kein Objektspeicher-Fall (total_bytes = 0: die Blöcke liegen dann nicht auf
  // unserer Platte) und kein Fall ohne ein einziges Home.
  if (!d?.enabled || d.total_bytes <= 0 || d.largest_home_bytes <= 0) return null;

  const eng = d.free_bytes < d.largest_home_bytes;
  const knapp = d.free_bytes < d.largest_home_bytes * 2;
  if (!eng && !knapp) return null;

  return (
    <Link
      to="/runners"
      className="card mb-4 no-underline"
      style={{ padding: "10px 16px", display: "block", borderColor: eng ? "var(--border-danger)" : undefined }}
    >
      <span className={`text-sm ${eng ? "danger-text" : ""}`}>
        {t(eng ? "dashboard.storeFull" : "dashboard.storeTight", {
          free: fmtBytes(d.free_bytes),
          largest: fmtBytes(d.largest_home_bytes),
        })}
      </span>
      <span className="muted text-xs" style={{ marginLeft: 8 }}>
        {t("dashboard.storeAction")}
      </span>
    </Link>
  );
}
