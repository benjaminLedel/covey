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

/* The workforce by department, and a search over it.
 *
 * The overview was a wall of tiles in filing order. That holds while you
 * know them all; from about a dozen on, the question is no longer "who is
 * here" but "who in support" and "where is Brunhilde". Both are answered by
 * the order the org chart already has — it was only not in the list.
 *
 * The search lives in the URL (?q=…): shared links and the back button
 * should work as they do everywhere else in this UI. */

// matches looks where someone would look: name, role, slug, state — and
// the department name, so that "support" also finds those who work there
// without it standing in their own name.
export function matches(a: Agent, deptName: string, q: string, states: string[] = []): boolean {
  if (states.length && !states.includes(stateOf(a))) return false;
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return [a.display_name, a.job_title, a.slug, a.status, deptName]
    .filter(Boolean)
    .some((v) => String(v).toLowerCase().includes(needle));
}

// stateOf sums up what the badge on the row shows. The platform knows five
// states, but "triggered" and "triage" are seconds on the way to working —
// as their own filters they would be buttons that almost never match.
export function stateOf(a: Agent): "working" | "sleeping" | "killed" {
  if (a.killed) return "killed";
  if (a.status === "sleeping") return "sleeping";
  return "working";
}

// Whoever runs stands on top. Otherwise the filing order decides, and that
// tells no one anything.
const RANK: Record<string, number> = { working: 0, triggered: 1, triage: 2, sleeping: 3, killed: 4 };
function byBusy(a: Agent, b: Agent): number {
  const ra = a.killed ? RANK.killed : (RANK[a.status] ?? 3);
  const rb = b.killed ? RANK.killed : (RANK[b.status] ?? 3);
  if (ra !== rb) return ra - rb;
  return a.display_name.localeCompare(b.display_name);
}

export type Group = { id: string | null; name: string; color: string; agents: Agent[] };

// groupByDepartment orders the workforce the way the org chart orders it:
// departments alphabetically, "no department" last — not because it is
// unimportant, but because it is no place where someone searches. Empty
// groups fall away: with an active search a department heading without hits
// is exactly the line that costs a second glance.
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

  // "/" jumps into the search field, Esc clears it — for a search you use
  // several times a day, reaching for the mouse is the movement you save.
  // Not while someone is typing elsewhere: otherwise the page eats the
  // character out of another field.
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

  // Rejecting means deleting, and that is defensible here: the draft never
  // worked, there is no run, no cost and no trace anyone would need later.
  // The brief it came from stays behind as a task — the reason is written
  // down there too.
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
        {/* The search at eye level with the heading, centred between it and
            the buttons: a row of its own below cost height for nothing. The
            count "2 in the organisation" stood beside it and answered a
            question nobody has — what counts stands at the departments. */}
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
            {/* The chips behind the field instead of below it: a second row
                pushed the workforce down, and both together are one
                statement — what I search for, and out of what. */}
            {(["working", "sleeping", "killed"] as const).map((st) => (
              <button
                key={st}
                className={`badge state st-${st}`}
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

      {/* The fill level of the home store. A store that grows quietly in the
          background is an operational risk — you notice it when the disk is
          full. Hence here, and with the warning before it instead of after. */}
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

      {/* Applications first and in a field of their own: an agent not yet
          hired does not work — among the others it would stand there like a
          colleague and still be none. The separation is therefore not
          decoration, but the statement. */}
      {drafts.length > 0 && (
        <section className="applications mb-5">
          <div className="flex items-baseline gap-2 mb-2">
            <h2 className="text-sm" style={{ fontWeight: 600 }}>{t("dashboard.applications")}</h2>
            <span className="badge st-draft">{drafts.length}</span>
            <span className="secondary text-xs">{t("dashboard.applicationsHint")}</span>
          </div>
          <div className="register">
            {drafts.map((a) => (
              <AgentRow
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
            {/* The department colour is the same as in the org chart — two
                views of the same order should look the same as well. */}
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
          <div className="register">
            <RegisterKopf />
            {g.agents.map((a) => (
              <AgentRow key={a.id} agent={a} />
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

/* The initials: letters and digits only.
   Otherwise "QA-Agent (GitLab)" becomes a circle reading "Q(" — the
   bracket is the first character of the second word. */
function initialsOf(name: string): string {
  return name
    .split(/[\s-]+/)
    .map((w) => w.replace(/[^\p{L}\p{N}]/gu, "")[0])
    .filter(Boolean)
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

/* An agent card.
 *
 * The state sits top right, the name left — the name may wrap without
 * taking the state's space: `min-w-0` on the text block, `shrink-0` on the
 * badge. Without that a two-line name pushed the badge into the heading.
 *
 * In the applications field the badge stays out (`labelled`): the box is
 * already called "applications", an "application" on every card in it says
 * nothing on top and costs exactly the space where it jams. */
/* A row in the register instead of a card in the grid.
 *
 * A workforce is a list of people, and a list you read in rows: name under
 * name, state under state, everything in one alignment. As a tile field
 * every value stood at a different place, and from a dozen agents on the
 * eye had to search every tile by itself.
 *
 * The columns are those of a personnel record: who, which slug, what it
 * runs on, what it may cost, how it stands. The state marker at the far
 * right stands at the same place for all — that is the point of an alignment. */
function AgentRow({
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
    <Link to={`/agents/${agent.id}`} className={`reg-row no-underline${draft ? " reg-row-draft" : ""}`}>
      <div className="avatar shrink-0">{initialsOf(agent.display_name)}</div>
      <div className="reg-wer min-w-0">
        {/* The job title is what the eye scans — "who in support" is
            answered by "software developer", not by `engineer-1`. */}
        <div className="font-medium text-sm reg-name">{agent.display_name}</div>
        {agent.job_title && <div className="secondary text-xs reg-name">{agent.job_title}</div>}
      </div>
      {/* The slug turns up in logs and webhooks, so it belongs in the
          register, but in the quiet column. */}
      <div className="reg-slug secondary text-xs mono">{agent.slug}</div>
      <div className="reg-engine secondary text-xs mono">{agent.runtime}</div>
      <div className="reg-budget secondary text-xs">
        {agent.budget_usd > 0 ? fmtUSD(agent.budget_usd) : <span className="reg-leer">—</span>}
      </div>
      <div className="reg-stand">
        {/* An agent waiting for the platform otherwise looks like one
            that is working. */}
        {agent.phase ? (
          <PhaseBadge phase={agent.phase} compact />
        ) : (
          !(draft && labelled) &&
          /* "Sleeping" and "does not come up" looked the same. An agent
             whose wake attempts fail therefore gets its own badge — and the
             reason stands in the title, instead of waiting in the raw data
             of the work record (#139). */
          (agent.wake_trouble && !draft && !agent.killed ? (
            <span
              className="badge state st-wake-failed"
              title={t("agent.wakeFailedWhy", {
                n: agent.wake_trouble.failures,
                err: agent.wake_trouble.error ?? "",
              })}
            >
              {t("status.wakeFailed")}
            </span>
          ) : (
            <span className={`badge state ${draft ? "st-draft" : `st-${agent.killed ? "killed" : agent.status}`}`}>
              {draft
                ? t("dashboard.draftBadge")
                : t(`status.${agent.killed ? "killed" : agent.status}`, agent.status)}
            </span>
          ))
        )}
      </div>
      <div className="reg-tun">
        {draft && (onHire || onReject) && (
          <>
            {onHire && (
              <button
                className="btn sm primary"
                onClick={(e) => {
                  e.preventDefault(); // the row is a link — the button is not
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
          </>
        )}
      </div>
    </Link>
  );
}

/* The column head of the register. It stands once per section and names
   what is in the alignment below it — a form says what belongs in its
   fields. For the applications it falls away: there are two rows there, and
   a head over two rows is a label without use. */
function RegisterKopf() {
  const { t } = useTranslation();
  return (
    <div className="reg-kopf" aria-hidden>
      <span />
      <span>{t("dashboard.colWho")}</span>
      <span>{t("dashboard.colSlug")}</span>
      <span>{t("dashboard.colEngine")}</span>
      <span>{t("dashboard.colBudget")}</span>
      <span>{t("dashboard.colState")}</span>
      <span />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Create modal with four paths: brief · template · manual · import
//
// The brief stands first and is the default path: it asks the one question
// someone can answer without knowing the platform. The manual path stays in
// full beside it — as the path for whoever knows exactly what they want,
// and as an emergency exit for when the HR department cannot work
// (spec/20).
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

// StoreLevel makes noise before space runs tight — and is silent otherwise.
// A line that stands there on every visit and never asks for anything is
// furniture: after two weeks you stop reading it, and the warning beside it
// too. Without cause, the fill level belongs under Administration → Runner,
// where the cleanup button stands.
//
// The criterion is deliberately no percentage. "90 % full" is still 200 GB
// on 2 TB and still four on 40 GB — the number says nothing about whether
// it is enough. The honest question is whether the next sync can land, and
// the largest home is the closest approach to that.
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
  // Not an object-storage case (total_bytes = 0: the blocks then do not lie
  // on our disk) and not a case without a single home.
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
