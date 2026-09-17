import { useState, type CSSProperties } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  api,
  post,
  patch,
  put,
  del,
  type Agent,
  type Voice,
  type RuntimeInfo,
  type SandboxService,
  type Workplace,
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
  // The sub-item stands in the URL: shareable links to the config of an
  // agent were possible before and should stay that way.
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
      {/* Sideways instead of on top: the settings are four independent areas
          with long forms, not views of the same thing. A menu at the side
          stays visible while scrolling and takes nothing from the height
          of the content. */}
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
  // The answer carries a warning when the chosen engine's CLI is nowhere to be
  // found here (#221). It refuses nothing — an image can catch up — but it
  // stands beside the picker instead of turning up as a failed task.
  const setRuntime = useMutation({
    mutationFn: (runtime: string) =>
      patch<{ ok: boolean; warning?: string }>(`/agents/${agent.id}/runtime`, { runtime }),
    onSuccess: invalidate,
  });
  // The organisation's voices (spec/24). Assigning writes the TONE.md into the
  // agent's config — a config version like any other, readable and revertible
  // in the same place as the rest.
  const voices = useQuery({
    queryKey: ["voices"],
    queryFn: () => api<Voice[]>("/voices"),
    retry: false,
  });
  const setVoice = useMutation({
    mutationFn: (voiceID: string) =>
      put<{ ok: boolean; note?: string }>(`/agents/${agent.id}/voice`, { voice_id: voiceID }),
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
  const setRecordingRetention = useMutation({
    mutationFn: (retention_days: number | null) =>
      patch(`/agents/${agent.id}/recording-retention`, { retention_days }),
    onSuccess: invalidate,
  });
  const setWarmSandbox = useMutation({
    mutationFn: (warm: boolean) => patch(`/agents/${agent.id}/warm-sandbox`, { warm }),
    onSuccess: invalidate,
  });
  const setRunnerTags = useMutation({
    mutationFn: (tags: string[]) => patch(`/agents/${agent.id}/runner-tags`, { runner_tags: tags }),
    onSuccess: invalidate,
  });
  const setSandboxImage = useMutation({
    mutationFn: (image: string) => patch(`/agents/${agent.id}/sandbox-image`, { sandbox_image: image }),
    onSuccess: invalidate,
  });
  const setServices = useMutation({
    mutationFn: (services: SandboxService[]) => patch(`/agents/${agent.id}/services`, { services }),
    onSuccess: invalidate,
  });
  // The workplaces come from the catalogue of the server (internal/sandbox),
  // not from a list here: a second list is the one missing the third profile.
  // The UI only adds what the server cannot know — translations, and that
  // "own image" is also an answer.
  const workplaces = useQuery({
    queryKey: ["workplaces"],
    queryFn: () => api<Workplace[]>("/workplaces"),
  });
  const profiles = workplaces.data ?? [];
  // Whether the agent sits on a profile or on a self-built image: both stand
  // in the same field, and the selection has to tell them apart. As long as
  // the catalogue is still loading, every value counts as a profile —
  // otherwise the field jumps to "own image" for the blink of an eye.
  const knownProfile =
    agent.sandbox_image === "" ||
    !workplaces.isSuccess ||
    profiles.some((p) => p.name === agent.sandbox_image);
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
  // The effort levels come from the engine, not from this file: an engine
  // without the knob should not be offered one. As long as the runtime list
  // is still loading, we show the row only if the agent already has a level
  // set — otherwise it flashes up and disappears again.
  const effortLevels = rtList.find((rt) => rt.name === agent.runtime)?.capabilities.effort_levels ?? [];
  /* The models the engine actually runs. Empty does NOT mean "none", but
     "not declared" — in front of a single provider the list belongs to the
     provider, and then it stays the free-text field as before. If an engine
     declares its ids (a gateway does that), this becomes a selection: a
     free-text field would offer models there that the instance lists but does
     not run — and there is no default to fall back onto. */
  const models = rtList.find((rt) => rt.name === agent.runtime)?.capabilities.models ?? [];
  // covey Doctor is named the same everywhere: name and slug belong to the
  // platform, not to the organisation. The block sits in the server (409) —
  // here it only sits visibly in front, so nobody types into a field whose
  // answer is already fixed.
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
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.group.identity")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("agent.settings.group.identityHint")}</p>
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
      <div style={{ ...row, borderBottom: "none" }}>
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
    </div>
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.group.thinking")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("agent.settings.group.thinkingHint")}</p>
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
      {setRuntime.data?.warning && (
        <p className="text-xs" style={{ color: "var(--warning, #b45309)", margin: "0 0 8px" }}>
          {setRuntime.data.warning}
        </p>
      )}
      <div style={row}>
        <span className="text-sm">{t("agent.settings.model")}</span>
        {models.length > 0 ? (
          <select
            key={`model:${agent.model}`}
            defaultValue={agent.model || ""}
            disabled={!editable || setModel.isPending}
            onChange={(e) => {
              if (e.target.value !== (agent.model || "")) setModel.mutate(e.target.value);
            }}
            className="mono"
          >
            {/* The first entry IS the default — the empty selection is no
                gap, but a statement: "what the engine takes itself". It
                stands on top for that, and names the model too. */}
            <option value="">{t("agent.settings.modelDefault", { model: models[0] })}</option>
            {models.map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        ) : (
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
        )}
        <span className="muted text-xs">
          {models.length > 0 ? t("agent.settings.modelHintFixed") : t("agent.settings.modelHint")}
        </span>
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
      <div style={{ ...row, borderBottom: "none" }}>
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
      {/* The voice this agent writes in. Only built ones can be chosen: a
          voice without a build has nothing to put into a TONE.md. */}
      <div style={row}>
        <span className="text-sm">{t("agent.settings.voice")}</span>
        <select
          value={agent.voice_id ?? ""}
          disabled={!editable || setVoice.isPending || !voices.isSuccess}
          onChange={(e) => setVoice.mutate(e.target.value)}
        >
          <option value="">{t("agent.settings.voiceNone")}</option>
          {(voices.data ?? [])
            .filter((v) => v.version > 0)
            .map((v) => (
              <option key={v.id} value={v.id}>
                {v.name}
                {v.language ? ` (${v.language})` : ""}
              </option>
            ))}
        </select>
        <span className="muted text-xs">{t("agent.settings.voiceHint")}</span>
      </div>
      {setVoice.isError && (
        <p className="text-xs" style={{ color: "var(--error)", margin: "0 0 8px" }}>
          {(setVoice.error as Error).message}
        </p>
      )}
      {setVoice.data?.note && (
        <p className="text-xs muted" style={{ margin: "0 0 8px" }}>{setVoice.data.note}</p>
      )}
    </div>
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.group.workplace")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("agent.settings.group.workplaceHint")}</p>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.sandboxImage")}</span>
        {/* An own image of the organisation is a valid value (spec/16),
            that is why a text field stays beside the selection: the list knows
            the profiles, not everything someone builds themselves. */}
        <div className="flex items-center gap-2">
          {/* While the catalogue loads, the field lacks the agent's option —
              the browser shows the first one, so "instance default" for an
              agent that sits on `dev`. Locked until the list is there: this
              is the only window where the display claims something other than
              the data state, and a field that would accept a change in this
              moment would also write it away. */}
          <select
            value={agent.sandbox_image}
            disabled={!editable || setSandboxImage.isPending || !workplaces.isSuccess}
            onChange={(e) => {
              if (e.target.value !== agent.sandbox_image) setSandboxImage.mutate(e.target.value);
            }}
          >
            <option value="">{t("agent.settings.sandboxImageDefault")}</option>
            {profiles.map((p) => (
              <option key={p.name} value={p.name}>
                {profileLabel(t, p)}
              </option>
            ))}
            {/* A value the list does not know: an own image from
                earlier, when this was still a text field. It stays selectable
                as long as it is set — otherwise the field would quietly write
                it away the next time someone looks at it. */}
            {!knownProfile && agent.sandbox_image !== "" && (
              <option value={agent.sandbox_image}>{agent.sandbox_image}</option>
            )}
          </select>
        </div>
        {/* The grid has three cells per row — warning and explanation
            therefore share the third, instead of breaking the row. */}
        <span className="text-xs flex flex-col gap-1">
          {/* A workplace whose image lies on no runner is selectable —
              it then wakes nothing, though. That belongs at the selection,
              not in the record of the first run that fails on it. */}
          {(() => {
            const chosen = profiles.find((p) => p.name === (agent.sandbox_image || defaultProfile(profiles)));
            if (!chosen || chosen.available !== false) return null;
            /* An image from the catalogue is not missing, it is only not
               here yet: it is published and pinned to the digest, so the
               runner pulls it at the first wake. To recommend building would
               be a recommendation nobody needs — and on a container
               installation one that cannot be followed. */
            if (chosen.source === "catalog") {
              return <span className="muted">{t("agent.settings.sandboxImagePulls")}</span>;
            }
            return (
              <span className="warn-text">
                {t("agent.settings.sandboxImageMissing", { image: chosen.image, build: chosen.build })}
              </span>
            );
          })()}
          {/* Which image the chosen workplace actually is, and
              where the address comes from. At the catalogue it is pinned to
              the digest and therefore long — it stands there anyway: it is
              the only thing that shows two instances start the same. */}
          {(() => {
            const chosen = profiles.find((p) => p.name === (agent.sandbox_image || defaultProfile(profiles)));
            if (!chosen?.image) return null;
            return (
              <span className="muted">
                {/* The tag is the readable name; the digest that actually
                    starts stands in title — sixty characters do not belong
                    in a line you read while walking past. */}
                <span className="mono" title={chosen.image}>
                  {chosen.tag || chosen.image}
                </span>
                {chosen.source && " — " + t(`agent.settings.sandboxImageSource.${chosen.source}`)}
              </span>
            );
          })()}
          <span className="muted">{t("agent.settings.sandboxImageHint")}</span>
        </span>
      </div>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.runnerTags")}</span>
        <input
          key={`rtags:${(agent.runner_tags ?? []).join(",")}`}
          defaultValue={(agent.runner_tags ?? []).join(", ")}
          placeholder="arm64, gpu"
          disabled={!editable || setRunnerTags.isPending}
          className="mono"
          onBlur={(e) => {
            const tags = e.target.value.split(",").map((x) => x.trim()).filter(Boolean);
            if (tags.join(",") !== (agent.runner_tags ?? []).join(",")) setRunnerTags.mutate(tags);
          }}
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
        />
        <span className="muted text-xs">{t("agent.settings.runnerTagsHint")}</span>
      </div>
      <div style={{ ...row, alignItems: "start" }}>
        <span className="text-sm" style={{ paddingTop: 6 }}>{t("agent.settings.services")}</span>
        <ServicesEditor
          /* Remounted as soon as the server delivers another state — like
             the runner tags next to it. A draft that survives a foreign
             change is the one that overwrites it. */
          key={JSON.stringify(agent.services ?? [])}
          services={agent.services ?? []}
          editable={editable && !setServices.isPending}
          t={t}
          onSave={(next) => setServices.mutate(next)}
        />
        <span className="muted text-xs">
          {t("agent.settings.servicesHint")}
          {setServices.isError && (
            <span className="danger-text" style={{ display: "block", marginTop: 4 }}>
              {String((setServices.error as Error)?.message ?? "")}
            </span>
          )}
        </span>
      </div>
      <div style={{ ...row, borderBottom: "none" }}>
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
    </div>
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.group.oversight")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("agent.settings.group.oversightHint")}</p>
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
        <span className="text-sm">{t("agent.settings.recordingRetention")}</span>
        <input
          key={`recret:${agent.recording_retention_days ?? ""}`}
          type="number"
          min={0}
          placeholder={t("agent.settings.recordingInherit")}
          defaultValue={agent.recording_retention_days ?? ""}
          className="mono"
          style={{ width: 90 }}
          disabled={!editable || setRecordingRetention.isPending}
          onBlur={(e) => {
            const roh = e.target.value.trim();
            const wert = roh === "" ? null : Number(roh);
            if (wert !== (agent.recording_retention_days ?? null)) setRecordingRetention.mutate(wert);
          }}
        />
        <span className="muted text-xs">{t("agent.settings.recordingRetentionHint")}</span>
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

function defaultProfile(profiles: Workplace[]): string {
  return profiles.find((p) => p.default)?.name ?? "";
}

// The catalogue delivers its description in English, like the target
// system plugins. Where the UI has a translation it takes that —
// and a profile added tomorrow stays readable without the language files.
function profileLabel(t: TFunction, p: Workplace): string {
  const translated = t(`agent.settings.sandboxProfile.${p.name}`, { defaultValue: "" });
  return translated || `${p.label} — ${p.description}`;
}

/* The services beside the sandbox (spec/16).

   A text field with `name=image` per line would have been cheaper, and that
   is exactly why there is none here: the name becomes a hostname on a shared
   runner, and a typo in it does not stand out as an error, but as a
   database that does not answer. Three separate fields show that they are
   three different things — and the server checks them again, because this
   UI is not the only way to it. */
function ServicesEditor({
  services,
  editable,
  t,
  onSave,
}: {
  services: SandboxService[];
  editable: boolean;
  t: TFunction;
  onSave: (next: SandboxService[]) => void;
}) {
  const [draft, setDraft] = useState<SandboxService[]>(services);

  const envText = (env?: Record<string, string>) =>
    Object.entries(env ?? {})
      .map(([k, v]) => `${k}=${v}`)
      .join(", ");
  const parseEnv = (text: string): Record<string, string> | undefined => {
    const out: Record<string, string> = {};
    for (const part of text.split(",")) {
      const eq = part.indexOf("=");
      if (eq <= 0) continue;
      const k = part.slice(0, eq).trim();
      if (k) out[k] = part.slice(eq + 1).trim();
    }
    return Object.keys(out).length ? out : undefined;
  };
  const change = (i: number, patchOne: Partial<SandboxService>) =>
    setDraft(draft.map((s, j) => (i === j ? { ...s, ...patchOne } : s)));
  // Saved is the whole list, not the single field: a service without an
  // image is not a half line, but an unfinished draft — it stays here
  // until it is complete.
  const save = (next: SandboxService[]) => {
    const clean = next.filter((s) => s.name.trim() && s.image.trim());
    if (JSON.stringify(clean) !== JSON.stringify(services)) onSave(clean);
  };

  return (
    <div className="flex flex-col gap-2">
      {draft.map((svc, i) => (
        <div key={i} className="flex gap-2 items-center">
          <input
            value={svc.name}
            placeholder={t("agent.settings.servicesName")}
            disabled={!editable}
            className="mono"
            style={{ width: 96 }}
            onChange={(e) => change(i, { name: e.target.value })}
            onBlur={() => save(draft)}
          />
          <input
            value={svc.image}
            placeholder="postgres:16"
            disabled={!editable}
            className="mono"
            style={{ flex: 1, minWidth: 120 }}
            onChange={(e) => change(i, { image: e.target.value })}
            onBlur={() => save(draft)}
          />
          <input
            defaultValue={envText(svc.env)}
            placeholder="KEY=value"
            disabled={!editable}
            className="mono"
            style={{ flex: 1, minWidth: 120 }}
            onBlur={(e) => {
              const next = draft.map((s, j) =>
                i === j ? { ...s, env: parseEnv(e.target.value) } : s,
              );
              setDraft(next);
              save(next);
            }}
          />
          <button
            type="button"
            className="ghost"
            disabled={!editable}
            title={t("agent.settings.servicesRemove")}
            onClick={() => {
              const next = draft.filter((_, j) => j !== i);
              setDraft(next);
              save(next);
            }}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="ghost text-xs"
        disabled={!editable}
        style={{ alignSelf: "start" }}
        onClick={() => setDraft([...draft, { name: "", image: "" }])}
      >
        + {t("agent.settings.servicesAdd")}
      </button>
    </div>
  );
}
