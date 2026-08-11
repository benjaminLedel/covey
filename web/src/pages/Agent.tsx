import { useState, useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useParams, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import { api, post, isDraft, type Agent, type Principal } from "../api";
import { AgentFiles } from "../components/AgentFiles";
import { AgentHome } from "../components/AgentHome";
import { HireDialog } from "../components/HireDialog";
import { canFiles, canKill, canManage, canSecrets } from "./agent/roles";
import { AgentTooling } from "./agent/Tooling";
import { AgentSettings } from "./agent/Settings";
import { CostBar } from "./agent/CostBar";
import { Performance } from "./agent/Performance";
import { LintFindings } from "./agent/LintFindings";
import { Backlog } from "./agent/Backlog";
import { Recording } from "./agent/Recording";
import { Memories } from "./agent/Memories";

// Die gueltigen Werte von ?tab=. Die zweite Gruppe ist zusammengelegt, als URL
// aber weiterhin gueltig: geteilte Links und Lesezeichen sollen nicht ins Leere
// laufen, sondern dort landen, wo der Inhalt jetzt wohnt (siehe MOVED unten).
// Dazu die englischen Namen der deutschen Slugs — wer "workspace" oder
// "settings" tippt, meint den Arbeitsplatz bzw. die Einstellungen.
const TABS = [
  "backlog", "recording", "memory", "dateien", "werkzeuge", "einstellungen",
  "heartbeat", "tools", "skills", "webhook", "config", "secrets", "egress", "dreams",
  "workspace", "files", "settings",
] as const;
type TabKey = (typeof TABS)[number];

// MOVED: alter Reiter → [neuer Reiter, Parametername, Wert].
const MOVED: Partial<Record<TabKey, [TabKey, string, string]>> = {
  heartbeat: ["einstellungen", "sub", "heartbeat"],
  webhook: ["einstellungen", "sub", "webhook"],
  config: ["einstellungen", "sub", "config"],
  secrets: ["einstellungen", "sub", "secrets"],
  egress: ["einstellungen", "sub", "egress"],
  settings: ["einstellungen", "sub", "general"],
  tools: ["werkzeuge", "sub", "mcp"],
  skills: ["werkzeuge", "sub", "skills"],
  dreams: ["memory", "view", "dreams"],
  workspace: ["dateien", "dir", ""],
  files: ["dateien", "dir", ""],
};

export default function AgentPage({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id } = useParams<{ id: string }>();
  const qc = useQueryClient();
  const agent = useQuery({ queryKey: ["agent", id], queryFn: () => api<Agent>(`/agents/${id}`) });
  // Tab-Zustand lebt in der URL (?tab=…) — echte Navigation: teilbare Links,
  // Browser-Vor/Zurück. Der memory-Tab führt zusätzlich ?page=<slug> mit.
  const [sp, setSp] = useSearchParams();
  // Nur bekannte Reiter zaehlen. Vorher fiel jeder unbekannte Wert durch das
  // `|| "backlog"` hindurch — es greift nur bei null und "" —, und ?tab=workspace
  // zeigte eine leere Seite statt des Arbeitsplatzes. Ein Link, den jemand von
  // Hand tippt oder aus einer aelteren Fassung mitbringt, soll irgendwo landen.
  const tab = (TABS as readonly string[]).includes(sp.get("tab") ?? "")
    ? (sp.get("tab") as TabKey)
    : "backlog";
  const setTab = (key: typeof tab) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("tab", key);
        n.delete("sub"); // Unterpunkt gehoert dem Reiter, den man verlaesst
        if (key !== "memory") n.delete("page"); // Wiki-Seite nur im memory-Tab
        if (key !== "dateien") {
          n.delete("dir"); // Ordner und Datei nur im Arbeitsplatz-Tab
          n.delete("file");
        }
        return n;
      },
      { replace: false },
    );
  const [recTask, setRecTask] = useState<{ id: string; title: string } | null>(null);
  const [hiring, setHiring] = useState(false);

  // Was man einmal einrichtet, wohnt unter den Einstellungen; was zusammen
  // gehoert, unter einem Reiter. Alte Links landen am neuen Ort statt auf dem
  // Backlog — geteilte Links und Lesezeichen sollen nicht ins Leere laufen.
  useEffect(() => {
    const to = MOVED[tab];
    if (!to) return;
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("tab", to[0]);
        if (to[2] === "") n.delete(to[1]);
        else n.set(to[1], to[2]);
        return n;
      },
      { replace: true },
    );
  }, [tab, setSp]);

  const act = useMutation({
    mutationFn: (action: string) => post(`/agents/${id}/${action}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agent", id] });
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });

  if (agent.isLoading) return null;
  if (agent.isError || !agent.data) return <p className="danger-text">{t("agent.notFound")}</p>;
  const a = agent.data;

  return (
    <div>
      <div className="text-sm secondary mb-3">
        <Link to="/" style={{ color: "inherit" }}>
          {t("agent.breadcrumb")}
        </Link>{" "}
        / <b style={{ color: "var(--text-primary)", fontWeight: 500 }}>{a.display_name}</b>
      </div>

      <div className="flex items-center gap-3 mb-5 flex-wrap">
        <h1 className="text-[22px]">{a.display_name}</h1>
        {isDraft(a) ? (
          <span className="badge st-draft">{t("dashboard.draftBadge")}</span>
        ) : (
          <span className={`badge st-${a.killed ? "killed" : a.status}`}>
            {t(`status.${a.killed ? "killed" : a.status}`, a.status)}
          </span>
        )}
        {(a.status === "working" || a.status === "triage" || a.status === "triggered") && (
          <span className="live-dot" title={t("agent.sandbox")} />
        )}
        <span className="muted text-xs mono">
          runtime: {a.runtime}
          {a.model && ` · ${a.model}`}
        </span>
        <span className="ml-auto" />
        {/* Ein Entwurf hat keinen ersten Tag — „wecken" wäre der falsche Knopf
            an der Stelle, an der „einstellen" steht. */}
        {canManage(me.Role) && !isDraft(a) && (
          <button className="btn sm" onClick={() => act.mutate("wake")}>
            {t("agent.wake")}
          </button>
        )}
        {canManage(me.Role) && isDraft(a) && (
          <button className="btn sm primary" onClick={() => setHiring(true)}>
            {t("hire.action")}
          </button>
        )}
        {/* Kill-Switch nur für einen, der laufen kann. Einen Entwurf zu stoppen
            ist keine Handlung — er hat nicht angefangen. */}
        {canKill(me.Role) && !isDraft(a) &&
          (a.killed ? (
            <button className="btn sm" onClick={() => act.mutate("resume")}>
              {t("agent.resume")}
            </button>
          ) : (
            <button className="btn sm danger" onClick={() => act.mutate("kill")} title="Kill-Switch">
              {t("agent.stop")}
            </button>
          ))}
      </div>

      {isDraft(a) && (
        <div className="card mb-4" style={{ borderStyle: "dashed" }}>
          <div className="text-sm" style={{ fontWeight: 600, marginBottom: 2 }}>
            {t("hire.bannerTitle")}
          </div>
          <p className="muted text-xs" style={{ maxWidth: 640 }}>{t("hire.bannerLead")}</p>
        </div>
      )}

      {hiring && <HireDialog agent={a} onClose={() => setHiring(false)} />}

      <CostBar agentId={a.id} budget={a.budget_usd} />

      <Performance agentId={a.id} />

      <LintFindings agentId={a.id} />

      <div className="flex gap-1 mb-4 mt-5" style={{ borderBottom: "0.5px solid var(--border)" }}>
        {(
          [
            ["backlog", t("agent.tabs.backlog")],
            ["recording", t("agent.tabs.recording")],
            ["memory", t("agent.tabs.memory")],
            ["dateien", t("agent.tabs.files")],
            ["werkzeuge", t("agent.tabs.toolsSkills")],
            ["einstellungen", t("agent.tabs.settings")],
          ] as const
        )
          // Der Arbeitsplatz zeigt, was im Home des Agenten liegt — das sehen
          // nur seine Verwalter und Security, nicht jede Rolle.
          .filter(([key]) => key !== "dateien" || canFiles(me.Role))
          .map(([key, label]) => (
          <button
            key={key}
            onClick={() => {
              if (key === "recording") setRecTask(null);
              setTab(key);
            }}
            className="btn sm"
            style={{
              border: "none",
              borderRadius: "8px 8px 0 0",
              borderBottom: tab === key ? "2px solid var(--text-accent)" : "2px solid transparent",
              color: tab === key ? "var(--text-primary)" : "var(--text-secondary)",
            }}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === "backlog" && (
        <Backlog
          agentId={a.id}
          canManage={canManage(me.Role)}
          onShowRecording={(id, title) => {
            setRecTask({ id, title });
            setTab("recording");
          }}
        />
      )}
      {tab === "recording" && (
        <Recording agentId={a.id} taskFilter={recTask} onClearFilter={() => setRecTask(null)} />
      )}
      {tab === "memory" && <Memories agentId={a.id} canManage={canManage(me.Role)} />}
      {tab === "dateien" && canFiles(me.Role) && (
        <>
          <AgentFiles agent={a} canWrite={canManage(me.Role)} />
          {/* Der Home-Store neben dem Dateibrowser (spec/16): was das Home
              wiegt, wovon nur dieser Agent es hält, und die Snapshots. */}
          <AgentHome agent={a} canWrite={canManage(me.Role)} />
        </>
      )}
      {tab === "werkzeuge" && (
        <AgentTooling
          agentId={a.id}
          canManage={canManage(me.Role)}
          canSecrets={canSecrets(me.Role)}
        />
      )}
      {tab === "einstellungen" && (
        <AgentSettings
          agent={a}
          editable={canManage(me.Role)}
          canManage={canManage(me.Role)}
          canSecrets={canSecrets(me.Role)}
          isSecurity={me.Role === "security"}
        />
      )}
    </div>
  );
}

// Werkzeuge buendeln, womit der Agent arbeitet: was er in den angebundenen
// Zielsystemen tun kann, welche MCP-Werkzeuge er davon nutzen darf und welche
// Skills er zieht. Als drei getrennte Reiter stand die Frage „was kann der
