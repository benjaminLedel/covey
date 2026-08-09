import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate } from "react-router";
import { useTranslation } from "react-i18next";
import { api, post, ApiError, type Agent, type AgentTemplate, type Principal } from "../api";
import { generateAgentName, slugify } from "../names";
import { GuidedCreate } from "./agents/GuidedCreate";
import { Modal } from "../components/Modal";
import { Onboarding } from "../components/Onboarding";

const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
const canSecurity = (role: string) => role === "platform_admin" || role === "security";

export default function Dashboard({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const fleet = useQuery({
    queryKey: ["fleet"],
    queryFn: () => api<{ fleet_killed: boolean }>("/fleet"),
  });
  const [showCreate, setShowCreate] = useState(false);

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
        <span className="muted text-sm">{t("dashboard.count", { count: agents.data?.length ?? 0 })}</span>
        <span className="ml-auto" />
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

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(250px, 1fr))" }}>
        {agents.data?.map((a) => (
          <AgentCard key={a.id} agent={a} />
        ))}
        {agents.data?.length === 0 && (
          <p className="muted">{t("dashboard.noAgents")}</p>
        )}
      </div>

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

function AgentCard({ agent }: { agent: Agent }) {
  const { t } = useTranslation();
  const initials = agent.display_name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <Link to={`/agents/${agent.id}`} className="card block no-underline" style={{ color: "inherit" }}>
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2.5">
          <div className="avatar">{initials}</div>
          <div>
            <div className="font-medium text-sm">{agent.display_name}</div>
            <div className="muted text-xs">{agent.slug}</div>
          </div>
        </div>
        <span className={`badge st-${agent.killed ? "killed" : agent.status}`}>
          {t(`status.${agent.killed ? "killed" : agent.status}`, agent.killed ? "gestoppt" : agent.status)}
        </span>
      </div>
      <div className="muted text-xs">
        Runtime: <span className="mono">{agent.runtime}</span>
        {agent.budget_usd > 0 && <> · {t("dashboard.budget")} {agent.budget_usd.toFixed(2)} $</>}
      </div>
    </Link>
  );
}

// ---------------------------------------------------------------------------
// Anlege-Modal mit drei Pfaden: Vorlage · Manuell · Bundle-Import
// ---------------------------------------------------------------------------

type CreatePath = "choose" | "template" | "manual" | "import";

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
    template: t("dashboard.fromTemplate"),
    manual: t("dashboard.manualCreate"),
    import: t("dashboard.importBundle"),
  };

  return (
    <Modal title={titles[path]} onClose={onClose} size={path === "template" ? "lg" : "md"}>
      {path === "choose" && <ChoosePath onPick={setPath} />}
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
                    Runtime: <span className="mono">{bundle?.agent?.runtime ?? "—"}</span>
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
              onClick={() => {
                const g = generateAgentName();
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
