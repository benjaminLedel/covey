import { useState, type CSSProperties } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  patch,
  del,
  type Agent,
  type RuntimeInfo,
} from "../../api";
import ProfileForm from "../../components/ProfileForm";
import { rollAgentName } from "../../names";

import { AgentEgress } from "./AgentEgress";
import { AgentSecrets } from "./AgentSecrets";
import { Config } from "./Config";
import { Heartbeats } from "./Heartbeats";
import { WebhookTrigger } from "./Webhook";

export function AgentSettings({
  agent,
  editable,
  canManage,
  canSecrets,
  isSecurity,
}: {
  agent: Agent;
  editable: boolean;
  canManage: boolean;
  canSecrets: boolean;
  isSecurity: boolean;
}) {
  const { t } = useTranslation();
  const [sp, setSp] = useSearchParams();
  // Der Unterpunkt steht in der URL: teilbare Links auf die Config eines
  // Agenten waren vorher moeglich und sollen es bleiben.
  const subs = [
    ["allgemein", t("agent.settings.subGeneral"), true],
    ["heartbeat", t("agent.tabs.heartbeat"), true],
    ["webhook", t("agent.tabs.webhook"), canManage],
    ["config", t("agent.tabs.config"), true],
    ["egress", t("agent.tabs.egress"), true],
    ["secrets", t("agent.tabs.secrets"), canSecrets],
  ] as const;
  const wanted = sp.get("sub") ?? "allgemein";
  const sub = subs.some(([k, , allowed]) => k === wanted && allowed) ? wanted : "allgemein";
  const setSub = (key: string) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("tab", "einstellungen");
        n.set("sub", key);
        return n;
      },
      { replace: false },
    );

  return (
    <div className="settings-panes">
      {/* Seitlich statt oben: die Einstellungen sind vier eigenstaendige Bereiche
          mit langen Formularen, keine Ansichten derselben Sache. Ein Menue an
          der Seite bleibt beim Scrollen sichtbar und nimmt dem Inhalt nichts
          von der Hoehe. */}
      <nav className="settings-nav" role="tablist">
        {subs
          .filter(([, , allowed]) => allowed)
          .map(([key, label]) => (
            <button
              key={key}
              role="tab"
              aria-selected={sub === key}
              className={`nav-item${sub === key ? " active" : ""}`}
              onClick={() => setSub(key)}
            >
              {label}
            </button>
          ))}
      </nav>
      <div className="min-w-0">
        {sub === "allgemein" && <AgentSettingsGeneral agent={agent} editable={editable} />}
        {sub === "heartbeat" && <Heartbeats agentId={agent.id} canManage={canManage} killed={agent.killed} />}
        {sub === "webhook" && canManage && <WebhookTrigger agentId={agent.id} />}
        {sub === "config" && (
          <Config
            agentId={agent.id}
            slug={agent.slug}
            displayName={agent.display_name}
            canManage={canManage}
            canExport={canManage || isSecurity}
          />
        )}
        {sub === "egress" && <AgentEgress agentId={agent.id} canEdit={canSecrets} />}
        {sub === "secrets" && canSecrets && <AgentSecrets agentId={agent.id} />}
      </div>
    </div>
  );
}

function AgentSettingsGeneral({ agent, editable }: { agent: Agent; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const runtimes = useQuery({
    queryKey: ["runtimes"],
    queryFn: () => api<RuntimeInfo[]>("/runtimes"),
  });
  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["agent", agent.id] });
    qc.invalidateQueries({ queryKey: ["agents"] });
  };
  const setName = useMutation({
    mutationFn: (displayName: string) => patch(`/agents/${agent.id}/name`, { display_name: displayName }),
    onSuccess: invalidate,
  });
  const setSlug = useMutation({
    mutationFn: (slug: string) => patch(`/agents/${agent.id}/slug`, { slug }),
    onSuccess: invalidate,
  });
  const setRuntime = useMutation({
    mutationFn: (runtime: string) => patch(`/agents/${agent.id}/runtime`, { runtime }),
    onSuccess: invalidate,
  });
  const setModel = useMutation({
    mutationFn: (model: string) => patch(`/agents/${agent.id}/model`, { model }),
    onSuccess: invalidate,
  });
  const setEffort = useMutation({
    mutationFn: (effort: string) => patch(`/agents/${agent.id}/effort`, { effort }),
    onSuccess: invalidate,
  });
  const setMaxTurns = useMutation({
    mutationFn: (maxTurns: number) => patch(`/agents/${agent.id}/max-turns`, { max_turns: maxTurns }),
    onSuccess: invalidate,
  });
  const setRecordingLevel = useMutation({
    mutationFn: (level: string) => patch(`/agents/${agent.id}/recording-level`, { level }),
    onSuccess: invalidate,
  });
  const setWarmSandbox = useMutation({
    mutationFn: (warm: boolean) => patch(`/agents/${agent.id}/warm-sandbox`, { warm }),
    onSuccess: invalidate,
  });
  const setBudget = useMutation({
    mutationFn: (budgetUSD: number) => post(`/agents/${agent.id}/budget`, { budget_usd: budgetUSD }),
    onSuccess: invalidate,
  });
  const deleteAgent = useMutation({
    mutationFn: () => del(`/agents/${agent.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      navigate("/");
    },
  });
  const [confirmDelete, setConfirmDelete] = useState(false);

  const anyError = [setName, setSlug, setRuntime, setModel, setEffort, setMaxTurns, setRecordingLevel, setBudget].find(
    (m) => m.isError,
  );

  const rtList = runtimes.data ?? [];
  // Die Denkaufwand-Stufen kommen von der Engine, nicht aus dieser Datei: eine
  // Engine ohne den Regler soll ihn auch nicht angeboten bekommen. Solange die
  // Runtime-Liste noch lädt, zeigen wir die Zeile nur, wenn der Agent bereits
  // eine Stufe gesetzt hat — sonst blitzt sie auf und verschwindet wieder.
  const effortLevels = rtList.find((rt) => rt.name === agent.runtime)?.capabilities.effort_levels ?? [];
  // Covey Doctor heisst ueberall gleich: Name und Slug gehoeren der
  // Plattform, nicht der Organisation. Die Sperre steht im Server (409) — hier
  // steht sie nur sichtbar davor, damit niemand gegen ein Feld tippt, dessen
  // Antwort schon feststeht.
  const isDoctor = agent.slug === "covey-doctor";
  const showEffort = effortLevels.length > 0 || !!agent.effort;
  const row: CSSProperties = {
    display: "grid",
    gridTemplateColumns: "180px minmax(200px, 320px) 1fr",
    alignItems: "center",
    gap: 12,
    padding: "10px 0",
    borderBottom: "0.5px solid var(--border)",
  };

  return (
    <>
    <div className="card mb-4" style={{ maxWidth: 760 }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.profile")}</div>
      <p className="muted text-xs mt-0 mb-3">{t("agent.settings.profileHint")}</p>
      <ProfileForm
        human={agent}
        endpoint={`/agents/${agent.id}/profile`}
        readOnly={!editable}
        onSaved={invalidate}
      />
    </div>
    <div className="card" style={{ maxWidth: 760, padding: "6px 18px 14px" }}>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.name")}</span>
        <span className="flex items-center gap-2">
          <input
            key={`name:${agent.display_name}`}
            defaultValue={agent.display_name}
            disabled={!editable || isDoctor || setName.isPending}
            onBlur={(e) => {
              const v = e.target.value.trim();
              if (v && v !== agent.display_name) setName.mutate(v);
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            style={{ flex: 1 }}
          />
          {editable && !isDoctor && (
            <button
              className="btn sm"
              title={t("agent.settings.rollDice")}
              disabled={setName.isPending}
              onClick={async () => setName.mutate((await rollAgentName()).name)}
            >
              🎲
            </button>
          )}
        </span>
        <span className="muted text-xs">
          {isDoctor ? t("agent.settings.fixedIdentity") : t("agent.settings.nameHint")}
        </span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.slug")}</span>
        <span className="flex items-center gap-2">
          <input
            key={`slug:${agent.slug}`}
            defaultValue={agent.slug}
            disabled={!editable || isDoctor || setSlug.isPending}
            className="mono"
            onBlur={(e) => {
              const v = e.target.value.trim();
              if (v && v !== agent.slug) setSlug.mutate(v);
              else e.target.value = agent.slug;
            }}
            onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
            style={{ flex: 1 }}
          />
        </span>
        <span className="muted text-xs">
          {setSlug.isError
            ? <span style={{ color: "var(--error)" }}>{String((setSlug.error as Error)?.message ?? t("agent.settings.slugError"))}</span>
            : isDoctor
              ? t("agent.settings.fixedIdentity")
              : t("agent.settings.slugHint")}
        </span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.runtime")}</span>
        <select
          value={agent.runtime}
          disabled={!editable || setRuntime.isPending}
          onChange={(e) => setRuntime.mutate(e.target.value)}
          className="mono"
        >
          {rtList.length === 0 && <option value={agent.runtime}>{agent.runtime}</option>}
          {rtList.map((rt) => (
            <option key={rt.name} value={rt.name}>
              {rt.name}
            </option>
          ))}
        </select>
        <span className="muted text-xs">{t("agent.settings.runtimeHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.model")}</span>
        <input
          key={`model:${agent.model}`}
          defaultValue={agent.model}
          placeholder={t("agent.settings.modelPlaceholder")}
          disabled={!editable || setModel.isPending}
          onBlur={(e) => {
            const v = e.target.value.trim();
            if (v !== agent.model) setModel.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.modelHint")}</span>
      </div>
      {showEffort && (
        <div style={row}>
          <span className="text-sm">{t("agent.settings.effort")}</span>
          <select
            key={`effort:${agent.effort}`}
            defaultValue={agent.effort || ""}
            disabled={!editable || setEffort.isPending}
            onChange={(e) => {
              if (e.target.value !== (agent.effort || "")) setEffort.mutate(e.target.value);
            }}
          >
            <option value="">{t("agent.settings.effortDefault")}</option>
            {effortLevels.map((lvl) => (
              <option key={lvl} value={lvl}>
                {lvl}
              </option>
            ))}
          </select>
          <span className="muted text-xs">{t("agent.settings.effortHint")}</span>
        </div>
      )}
      <div style={row}>
        <span className="text-sm">{t("agent.settings.maxTurns")}</span>
        <input
          key={`turns:${agent.max_turns}`}
          type="number"
          min={0}
          defaultValue={agent.max_turns || ""}
          placeholder={t("agent.settings.maxTurnsPlaceholder")}
          disabled={!editable || setMaxTurns.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Math.trunc(Number(e.target.value) || 0));
            if (v !== agent.max_turns) setMaxTurns.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.maxTurnsHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.recordingLevel")}</span>
        <select
          key={`reclvl:${agent.recording_level}`}
          defaultValue={agent.recording_level || ""}
          disabled={!editable || setRecordingLevel.isPending}
          onChange={(e) => {
            if (e.target.value !== (agent.recording_level || "")) setRecordingLevel.mutate(e.target.value);
          }}
        >
          <option value="">{t("agent.settings.recordingInherit")}</option>
          <option value="minimal">{t("agent.settings.recordingMinimal")}</option>
          <option value="standard">{t("agent.settings.recordingStandard")}</option>
          <option value="full">{t("agent.settings.recordingFull")}</option>
        </select>
        <span className="muted text-xs">{t("agent.settings.recordingHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.warmSandbox")}</span>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={agent.warm_sandbox}
            disabled={!editable || setWarmSandbox.isPending}
            onChange={(e) => setWarmSandbox.mutate(e.target.checked)}
          />
          {agent.warm_sandbox ? t("agent.settings.warmOn") : t("agent.settings.warmOff")}
        </label>
        <span className="muted text-xs">{t("agent.settings.warmHint")}</span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.budget")}</span>
        <input
          key={`budget:${agent.budget_usd}`}
          type="number"
          min={0}
          step="0.01"
          defaultValue={agent.budget_usd || ""}
          placeholder={t("agent.settings.budgetPlaceholder")}
          disabled={!editable || setBudget.isPending}
          onBlur={(e) => {
            const v = Math.max(0, Number(e.target.value) || 0);
            if (v !== agent.budget_usd) setBudget.mutate(v);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          className="mono"
        />
        <span className="muted text-xs">{t("agent.settings.budgetHint")}</span>
      </div>
      <div style={{ ...row, borderBottom: "none" }}>
        <span className="text-sm">{t("agent.settings.diagnostics")}</span>
        <a
          className="btn sm"
          href={`/api/v1/agents/${agent.id}/diagnostics`}
          download={`diagnostics-${agent.slug}.json`}
        >
          {t("agent.settings.diagnosticsExport")}
        </a>
        <span className="muted text-xs">{t("agent.settings.diagnosticsHint")}</span>
      </div>
      {!editable && (
        <p className="muted text-xs mt-2">{t("agent.settings.readOnly")}</p>
      )}
      {anyError && <p className="danger-text text-xs mt-2">{String(anyError.error)}</p>}
      {editable && (
        <div style={{ marginTop: 24, paddingTop: 14, borderTop: "0.5px solid var(--border)" }}>
          <p className="text-xs muted mb-2">{t("agent.settings.dangerZone")}</p>
          {!confirmDelete ? (
            <button className="btn sm danger" onClick={() => setConfirmDelete(true)}>
              {t("agent.settings.deleteAgent")}
            </button>
          ) : (
            <div className="flex items-center gap-3">
              <span className="text-xs" style={{ color: "var(--danger, #b91c1c)" }}>
                {t("agent.settings.deleteConfirm", { name: agent.display_name })}
              </span>
              <button
                className="btn sm danger"
                disabled={deleteAgent.isPending}
                onClick={() => deleteAgent.mutate()}
              >
                {t("agent.settings.deleteYes")}
              </button>
              <button className="btn sm" onClick={() => setConfirmDelete(false)}>
                {t("agent.settings.cancel")}
              </button>
            </div>
          )}
          {deleteAgent.isError && (
            <p className="danger-text text-xs mt-2">{String((deleteAgent.error as Error)?.message ?? "Fehler")}</p>
          )}
        </div>
      )}
    </div>
    </>
  );
}
