import { useState, type CSSProperties } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  api,
  post,
  patch,
  del,
  type Agent,
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
  // Die Arbeitsplätze kommen aus dem Katalog des Servers (internal/sandbox),
  // nicht aus einer Liste hier: eine zweite Liste ist die, in der das dritte
  // Profil fehlt. Die Oberfläche steuert nur bei, was der Server nicht wissen
  // kann — Übersetzungen, und dass „eigenes Image" auch eine Antwort ist.
  const workplaces = useQuery({
    queryKey: ["workplaces"],
    queryFn: () => api<Workplace[]>("/workplaces"),
  });
  const profiles = workplaces.data ?? [];
  // Ob der Agent auf einem Profil sitzt oder auf einem selbst gebauten Image:
  // beides steht im selben Feld, und die Auswahl muss das auseinanderhalten.
  // Solange der Katalog noch lädt, gilt jeder Wert als Profil — sonst springt
  // das Feld für einen Wimpernschlag auf „eigenes Image".
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
  // Die Denkaufwand-Stufen kommen von der Engine, nicht aus dieser Datei: eine
  // Engine ohne den Regler soll ihn auch nicht angeboten bekommen. Solange die
  // Runtime-Liste noch lädt, zeigen wir die Zeile nur, wenn der Agent bereits
  // eine Stufe gesetzt hat — sonst blitzt sie auf und verschwindet wieder.
  const effortLevels = rtList.find((rt) => rt.name === agent.runtime)?.capabilities.effort_levels ?? [];
  /* Modelle, die die Engine wirklich fährt. Leer heißt NICHT „keine", sondern
     „nicht deklariert" — vor einem einzelnen Anbieter gehört die Liste dem
     Anbieter, und dann bleibt es das Freitextfeld wie bisher. Deklariert eine
     Engine ihre Ids (ein Gateway tut das), wird daraus eine Auswahl: ein
     Freitextfeld böte dort Modelle an, die die Instanz zwar listet, aber nicht
     fährt — und es gibt keinen Default, auf den man zurückfallen könnte. */
  const models = rtList.find((rt) => rt.name === agent.runtime)?.capabilities.models ?? [];
  // covey Doctor heisst ueberall gleich: Name und Slug gehoeren der
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
            {/* Der erste Eintrag IST der Default — die leere Auswahl ist damit
                keine Lücke, sondern eine Aussage: „was die Engine selbst
                nimmt". Sie steht deshalb oben und nennt das Modell mit. */}
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
    </div>
    <div className="card mb-4" style={{ maxWidth: 760, padding: "14px 18px 4px" }}>
      <div className="text-sm font-medium mb-1">{t("agent.settings.group.workplace")}</div>
      <p className="muted text-xs mt-0 mb-2">{t("agent.settings.group.workplaceHint")}</p>
      <div style={row}>
        <span className="text-sm">{t("agent.settings.sandboxImage")}</span>
        {/* Ein eigenes Image der Organisation ist ein gültiger Wert (spec/16),
            deshalb bleibt neben der Auswahl ein Textfeld: die Liste kennt die
            Profile, nicht alles, was jemand selbst baut. */}
        <div className="flex items-center gap-2">
          {/* Solange der Katalog lädt, fehlt dem Feld die Option des Agenten —
              der Browser zeigt dann die erste an, also „Voreinstellung der
              Instanz" für einen Agenten, der auf `dev` sitzt. Gesperrt, bis die
              Liste da ist: das ist das einzige Fenster, in dem die Anzeige
              etwas anderes behauptet als der Datenstand, und ein Feld, das in
              diesem Moment eine Änderung annähme, schriebe sie auch weg. */}
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
            {/* Ein Wert, den die Liste nicht kennt: ein eigenes Image von
                früher, als es hier noch ein Textfeld gab. Er bleibt wählbar,
                solange er gesetzt ist — sonst schriebe das Feld ihn beim
                nächsten Blick still weg. */}
            {!knownProfile && agent.sandbox_image !== "" && (
              <option value={agent.sandbox_image}>{agent.sandbox_image}</option>
            )}
          </select>
        </div>
        {/* Das Raster hat drei Zellen je Zeile — Warnung und Erklärung teilen
            sich deshalb die dritte, statt die Zeile umbrechen zu lassen. */}
        <span className="text-xs flex flex-col gap-1">
          {/* Ein Arbeitsplatz, dessen Image auf keinem Runner liegt, ist
              wählbar — er weckt dann aber nichts. Das gehört an die Auswahl und
              nicht in die Aufzeichnung des ersten Laufs, der daran scheitert. */}
          {(() => {
            const chosen = profiles.find((p) => p.name === (agent.sandbox_image || defaultProfile(profiles)));
            if (!chosen || chosen.available !== false) return null;
            /* Ein Image aus dem Katalog fehlt nicht, es liegt nur noch nicht
               hier: Es ist veröffentlicht und auf den Digest gepinnt, also
               zieht der Runner es beim ersten Wecken. Zum Bauen zu raten wäre
               ein Rat, den man nicht braucht — und auf einer
               Container-Installation einer, den man nicht befolgen kann. */
            if (chosen.source === "catalog") {
              return <span className="muted">{t("agent.settings.sandboxImagePulls")}</span>;
            }
            return (
              <span className="warn-text">
                {t("agent.settings.sandboxImageMissing", { image: chosen.image, build: chosen.build })}
              </span>
            );
          })()}
          {/* Welches Image der gewählte Arbeitsplatz tatsächlich ist, und
              woher die Adresse kommt. Beim Katalog ist sie auf den Digest
              gepinnt und darum lang — sie steht trotzdem da: sie ist das
              Einzige, woran man sieht, dass zwei Instanzen dasselbe starten. */}
          {(() => {
            const chosen = profiles.find((p) => p.name === (agent.sandbox_image || defaultProfile(profiles)));
            if (!chosen?.image) return null;
            return (
              <span className="muted">
                {/* Der Tag ist der lesbare Name; der Digest, der tatsächlich
                    startet, steht im title — sechzig Zeichen gehören nicht in
                    eine Zeile, die man im Vorbeigehen liest. */}
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
          /* Neu montiert, sobald der Server einen anderen Stand liefert — wie
             die Runner-Tags daneben. Ein Entwurf, der eine fremde Änderung
             überlebt, ist der, der sie überschreibt. */
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

// Der Katalog liefert seine Beschreibung englisch, wie die Zielsystem-Plugins
// auch. Wo die Oberfläche eine Übersetzung hat, nimmt sie die — und ein morgen
// hinzugefügtes Profil ist trotzdem lesbar, ohne die Sprachdateien anzufassen.
function profileLabel(t: TFunction, p: Workplace): string {
  const translated = t(`agent.settings.sandboxProfile.${p.name}`, { defaultValue: "" });
  return translated || `${p.label} — ${p.description}`;
}

/* Die Dienste neben der Sandbox (spec/16).

   Ein Textfeld mit `name=image` je Zeile wäre billiger gewesen, und genau
   deshalb steht hier keins: Der Name wird ein Hostname auf einem geteilten
   Runner, und ein Tippfehler darin fällt nicht als Fehler auf, sondern als
   Datenbank, die nicht antwortet. Drei getrennte Felder zeigen, dass es drei
   verschiedene Dinge sind — und der Server prüft sie noch einmal, denn diese
   Oberfläche ist nicht der einzige Weg zu ihm. */
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
  // Gespeichert wird die ganze Liste, nicht das einzelne Feld: Ein Dienst ohne
  // Image ist keine halbe Zeile, sondern ein unfertiger Entwurf — der bleibt
  // hier stehen, bis er vollständig ist.
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
