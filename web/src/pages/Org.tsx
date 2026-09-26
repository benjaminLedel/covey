import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api, patch, buildInfo, OFFICE_FURNISHINGS,
  type Agent, type AgentSystem, type OfficeFurnishing as Furnishing, type OrgChart, type Organization,
} from "../api";
import { OrgChart as OrgChartView } from "../components/orgchart/OrgChart";

/* How densely the office is furnished (#325). One of three words, set for
   the organisation: the density is a property of the building, and the
   building belongs to the organisation — remembered in a browser, every
   person saw a different house and the choice was lost on another device.
   The office reads it from the same query. */
export function OfficeFurnishing() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });
  const save = useMutation({
    mutationFn: (furnishing: Furnishing) => patch<{ ok: boolean }>("/org/office", { furnishing }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["own-org"] }),
  });
  const team = useTeamSurface();
  // The office is part of the team surface; without it there is nothing to furnish (#328).
  if (!own.data || !team.data?.enabled) return null;
  const stufe: Record<Furnishing, string> = {
    sparse: t("team.ausstattungSparsam"),
    normal: t("team.ausstattungNormal"),
    rich: t("team.ausstattungUeppig"),
  };
  return (
    <div className="card mb-4">
      <div className="flex items-baseline gap-2 mb-1">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>{t("org.office.label")}</h2>
      </div>
      <div className="flex gap-2 mb-2" role="group" aria-label={t("org.office.label")}>
        {OFFICE_FURNISHINGS.map((f) => (
          <button
            key={f}
            type="button"
            className={`btn sm${own.data.office_furnishing === f ? " primary" : ""}`}
            aria-pressed={own.data.office_furnishing === f}
            disabled={save.isPending}
            onClick={() => save.mutate(f)}
          >
            {stufe[f]}
          </button>
        ))}
      </div>
      <p className="muted text-xs" style={{ maxWidth: 640 }}>{t("org.office.hint")}</p>
    </div>
  );
}

// `head`: the band at the top of the org chart's frame; `card` (default):
// the boxed card among the other settings on the Administration page.
export function CompanyDescription({ variant = "card" }: { variant?: "card" | "head" }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });
  const [draft, setDraft] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: (description: string) => patch<{ ok: boolean }>("/org/description", { description }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["own-org"] });
      setDraft(null);
    },
  });

  if (!own.data) return null;
  const text = own.data.description ?? "";
  const editing = draft !== null;

  return (
    <div className={variant === "head" ? "orgc-head" : "card mb-4"}>
      <div className="flex items-baseline gap-2 mb-1">
        <h2 className={variant === "head" ? "orgc-head-name" : "text-sm"} style={{ fontWeight: 600 }}>{own.data.name}</h2>
        <span className="muted text-xs">{t("org.company.label")}</span>
        {!editing && (
          <button className="btn sm ml-auto" style={{ border: "none" }} onClick={() => setDraft(text)}>
            {text ? t("org.company.edit") : t("org.company.add")}
          </button>
        )}
      </div>
      {!editing && (
        <p className={`text-xs ${text ? "" : "muted"}`} style={{ maxWidth: 640 }}>
          {text || t("org.company.empty")}
        </p>
      )}
      {editing && (
        <form
          onSubmit={e => { e.preventDefault(); save.mutate(draft); }}
          style={{ maxWidth: 640 }}
        >
          <textarea
            rows={4}
            value={draft}
            autoFocus
            placeholder={t("org.company.placeholder")}
            onChange={e => setDraft(e.target.value)}
          />
          <div className="muted text-xs" style={{ margin: "3px 0 6px" }}>{t("org.company.hint")}</div>
          <div className="flex gap-2">
            <button className="btn sm primary" type="submit" disabled={save.isPending}>
              {t("org.company.save")}
            </button>
            <button className="btn sm" type="button" onClick={() => setDraft(null)}>
              {t("modal.cancel")}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

/* Where the source code of this platform lives (spec/21).
 *
 * covey Doctor makes its most valuable finding where a config cannot fix it
 * — and it is the only one in the organisation that has seen it in several
 * colleagues at once. So that this becomes a diagnosis instead of a symptom,
 * it reads the source code; so that the report arrives, it files into the
 * same repository.
 *
 * WHICH repository is decided by the organisation. An instance against the
 * public GitHub mirror would otherwise have an agent that writes issues where
 * the whole world reads along. That is why this stands with the master data
 * and not in a prompt. Empty means: there is no third layer, and the prompt
 * says nothing of it either.
 *
 * The card lives under Verwaltung → Organisation, beside the company
 * description and the recording settings — the surface for settings, the
 * not-yet-used ones included. It used to stand on the org chart as well,
 * first always, then only where covey Doctor existed; a setting for one
 * reader between the people and departments of a chart about escalation is
 * a setting in the wrong place either way (#178). The component stays in
 * this file because the Administration page imports it from here.
 *
 * And it was half of the setup: without a line in the ACCESS.md of
 * covey Doctor the section stays out of his prompt, and nothing of that
 * stood on the card, but in the small print of the form. Whoever saved
 * did not see whether it had worked. Now the state stands where the
 * result stands (RepoZugang). */

/* Whether the setting works at all — read from the same source the prompt is
   built from: `access` is the line in the ACCESS.md of covey Doctor,
   `enabled` the release of the plugin for the organisation. */
function RepoZugang({
  doctor,
  system,
  systeme,
  canFile,
}: {
  doctor: Agent;
  system: string;
  systeme?: AgentSystem[] | null;
  canFile: boolean;
}) {
  const { t } = useTranslation();
  if (!systeme) return null; // not loaded yet — rather nothing than a guess
  const eintrag = systeme.find((s) => s.name === system);
  const zumAgenten = (
    <Link to={`/agents/${doctor.id}?tab=config`}>{doctor.display_name}</Link>
  );

  /* Filing and reading hang on different things, since the platform files by
     itself: the account in the secrets carries the filing, the line in the
     ACCESS.md the reading. Without an account nothing is reported — that is
     the state that looked like a finished setup for eleven days. */
  if (!canFile) {
    return (
      <p className="warn-text text-xs" style={{ maxWidth: 640 }}>
        {t("org.repo.stateNoAccount", { system, key: `${system}_token` })}{" "}
        <Link to="/secrets">{t("nav.secrets")}</Link>
      </p>
    );
  }

  if (!eintrag) {
    return (
      <p className="warn-text text-xs" style={{ maxWidth: 640 }}>
        {t("org.repo.stateUnknownSystem", { system })}
      </p>
    );
  }
  if (!eintrag.enabled) {
    return (
      <p className="warn-text text-xs" style={{ maxWidth: 640 }}>
        {t("org.repo.stateDisabled", { system: eintrag.label || eintrag.name })}{" "}
        <Link to="/targets">{t("nav.targets")}</Link>
      </p>
    );
  }
  if (!eintrag.access) {
    return (
      <p className="warn-text text-xs" style={{ maxWidth: 640 }}>
        {t("org.repo.stateNoAccess", { system: eintrag.label || eintrag.name })}{" "}
        {zumAgenten}
      </p>
    );
  }
  return (
    <p className="muted text-xs" style={{ maxWidth: 640 }}>
      {t("org.repo.stateOk")} {zumAgenten}
    </p>
  );
}

/* "Off" — the same character as agents.RepoOff in the backend. It needed a
   value of its own, since the default exists: "empty" used to mean "not at
   all" and now means "the project this program comes from". */
const REPO_AUS = "-";

const useTeamSurface = () =>
  useQuery({
    queryKey: ["org-team-surface"],
    queryFn: () => api<{ enabled: boolean }>("/org/team-surface"),
  });

/* The switch for the team surface (#328). Off by default while it is in beta.
 * It decides the start page for everyone in the organisation, so it sits with
 * the organisation's settings and not in a person's preferences; switching it
 * refetches /auth/me, which is where the shell reads it. */
export function TeamSurfaceSettings() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const team = useTeamSurface();
  const setEnabled = useMutation({
    mutationFn: (enabled: boolean) => patch<{ enabled: boolean }>("/org/team-surface", { enabled }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["org-team-surface"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
  });

  if (!team.data) return null;

  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>
        {t("org.teamSurface.title")}{" "}
        <span className="muted text-xs" style={{ fontWeight: 500 }}>· {t("org.teamSurface.beta")}</span>
      </h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("org.teamSurface.hint")}</p>
      <select
        key={`team:${team.data.enabled}`}
        defaultValue={team.data.enabled ? "on" : "off"}
        disabled={setEnabled.isPending}
        onChange={(e) => setEnabled.mutate(e.target.value === "on")}
      >
        <option value="off">{t("org.teamSurface.off")}</option>
        <option value="on">{t("org.teamSurface.on")}</option>
      </select>
    </div>
  );
}

/* Whether a push notification may carry the first line of what was said
 * (#379). Off, a notification says only who and what — "Bea has a question" —
 * and nothing of the content leaves the instance. On, the first line goes
 * along, and with it through Apple and, where used, the relay. */
export function PushSettings() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const team = useTeamSurface();
  const pushQ = useQuery({ queryKey: ["org-push"], queryFn: () => api<{ preview: boolean }>("/org/push") });
  const setPreview = useMutation({
    mutationFn: (preview: boolean) => patch<{ preview: boolean }>("/org/push", { preview }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org-push"] }),
  });

  if (!pushQ.data || !team.data?.enabled) return null;

  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("org.push.title")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("org.push.hint")}</p>
      <select
        key={`push:${pushQ.data.preview}`}
        defaultValue={pushQ.data.preview ? "on" : "off"}
        disabled={setPreview.isPending}
        onChange={(e) => setPreview.mutate(e.target.value === "on")}
      >
        <option value="off">{t("org.push.off")}</option>
        <option value="on">{t("org.push.on")}</option>
      </select>
    </div>
  );
}

/* Der Schalter, der entscheidet, ob eine Nachricht im Team zwangsläufig eine
 * Aufgabe wird (#302).
 *
 * Er steht hier bei den organisationsweiten Einstellungen und nicht am
 * einzelnen Agenten: Was ein Zug in der Control Plane kosten darf und ob es
 * ihn überhaupt gibt, ist eine Entscheidung der Organisation, nicht eine je
 * Kollege. Ohne Zugangsdaten in der Control Plane bleibt er ein Schalter ohne
 * Wirkung — dann sagt die Zeile das, statt ihn anzubieten.
 */
export function TriageSettings() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const team = useTeamSurface();
  const triage = useQuery({
    queryKey: ["org-triage"],
    queryFn: () => api<{ mode: string; available: boolean }>("/org/chat-triage"),
  });
  const setMode = useMutation({
    mutationFn: (mode: string) => patch<{ mode: string }>("/org/chat-triage", { mode }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org-triage"] }),
  });

  /* Without the team surface there are no messages to decide about (#328). */
  if (!triage.data || !team.data?.enabled) return null;
  const an = triage.data.mode === "on";

  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("org.triage.title")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("org.triage.hint")}</p>

      <div className="flex items-center gap-3 flex-wrap">
        <select
          key={`triage:${triage.data.mode}`}
          defaultValue={triage.data.mode}
          disabled={setMode.isPending || !triage.data.available}
          onChange={(e) => setMode.mutate(e.target.value)}
        >
          <option value="off">{t("org.triage.off")}</option>
          <option value="on">{t("org.triage.on")}</option>
        </select>
        {!triage.data.available && <span className="muted text-xs">{t("org.triage.noCredential")}</span>}
        {triage.data.available && an && <span className="muted text-xs">{t("org.triage.cost")}</span>}
      </div>
    </div>
  );
}

// The recording: how deep things are written along, and how long the verbatim
// history stays (spec/06). Both org-wide, both overridable on the agent — the
// depth only upward, the deadline only longer. An agent that could shorten its
// own trail would be exactly the gap that an org-wide setting is meant to
// close.
export function RecordingSettings() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const rec = useQuery({
    queryKey: ["org-recording"],
    queryFn: () => api<{ level: string; retention_days: number }>("/org/recording-level"),
  });

  const setLevel = useMutation({
    mutationFn: (level: string) => patch<{ ok: boolean }>("/org/recording-level", { level }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org-recording"] }),
  });
  const setRetention = useMutation({
    mutationFn: (retention_days: number) =>
      patch<{ ok: boolean }>("/org/recording-retention", { retention_days }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["org-recording"] }),
  });

  if (!rec.data) return null;

  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("org.recording.title")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("org.recording.hint")}</p>

      <div className="flex items-center gap-3 flex-wrap mb-2">
        <span className="text-sm">{t("org.recording.level")}</span>
        <select
          key={`orglvl:${rec.data.level}`}
          defaultValue={rec.data.level}
          disabled={setLevel.isPending}
          onChange={(e) => setLevel.mutate(e.target.value)}
        >
          <option value="minimal">{t("agent.settings.recordingMinimal")}</option>
          <option value="standard">{t("agent.settings.recordingStandard")}</option>
          <option value="full">{t("agent.settings.recordingFull")}</option>
        </select>
      </div>

      <div className="flex items-center gap-3 flex-wrap">
        <span className="text-sm">{t("org.recording.retention")}</span>
        <input
          key={`orgret:${rec.data.retention_days}`}
          type="number"
          min={0}
          defaultValue={rec.data.retention_days}
          className="mono"
          style={{ width: 90 }}
          disabled={setRetention.isPending}
          onBlur={(e) => {
            const v = Number(e.target.value);
            if (v !== rec.data!.retention_days) setRetention.mutate(v);
          }}
        />
        <span className="muted text-xs">{t("org.recording.retentionHint")}</span>
      </div>
      <p className="muted text-xs">{t("org.recording.keeps")}</p>
    </div>
  );
}

export function PlatformRepo() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });
  /* Where this program comes from — the default. It does not stand in the
     surface, but comes from the server (buildinfo), so that a fork carries its
     own project and not that of the origin. */
  const build = useQuery({ queryKey: ["build"], queryFn: buildInfo, staleTime: Infinity });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<{ name: string; label: string; enabled: boolean }[] | null>("/targets"),
  });
  /* The same key as on the page around it: the query does not run a second
     time, the card only reads along. */
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });
  const doctor = (chart.data?.agents ?? []).find((a) => a.slug === "covey-doctor");
  /* What accesses covey Doctor HAS — the same source his prompt is built from
     (access = a line in his ACCESS.md). */
  const systeme = useQuery({
    queryKey: ["agent-systems", doctor?.id],
    queryFn: () => api<AgentSystem[] | null>(`/agents/${doctor!.id}/systems`),
    enabled: !!doctor,
  });
  const [editing, setEditing] = useState(false);
  const [system, setSystem] = useState("");
  const [project, setProject] = useState("");
  const [error, setError] = useState("");

  const save = useMutation({
    /* Default and "off" carry no project — what stood in the field before
       someone switched the target system is not saved along. */
    mutationFn: () =>
      patch<{ ok: boolean }>("/org/platform-repo", {
        system,
        project: system === "" || system === REPO_AUS ? "" : project,
      }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["own-org"] });
      setEditing(false);
      setError("");
    },
    onError: (e: Error) => setError(e.message),
  });

  /* On the org chart without covey Doctor: no card. Whoever hires him finds
     it there with him — in the master data it stands anyway. */
  if (!own.data) return null;

  /* The same resolution as in the backend (agents.PlatformRepo): own
     repository, otherwise the project of this platform, unless it is switched
     off. Two places, one rule — the card should show what applies, not what
     is stored. */
  const eigenes = own.data.platform_repo_system && own.data.platform_repo_project;
  const aus = own.data.platform_repo_system === REPO_AUS;
  const gilt = aus
    ? null
    : eigenes
      ? { system: own.data.platform_repo_system!, project: own.data.platform_repo_project! }
      : build.data?.source_system && build.data.source_project
        ? { system: build.data.source_system, project: build.data.source_project }
        : null;

  // Only connected target systems: an address on a system without a
  // credential would only come to nothing at the checkout.
  const wahl = (targets.data ?? []).filter((x) => x.enabled);

  const start = () => {
    setSystem(own.data!.platform_repo_system ?? "");
    setProject(own.data!.platform_repo_project ?? "");
    setEditing(true);
  };

  return (
    <div className="card mb-4">
      <div className="flex items-baseline gap-2 mb-1">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>{t("org.repo.title")}</h2>
        {!editing && (
          <button className="btn sm ml-auto" style={{ border: "none" }} onClick={start}>
            {t("org.company.edit")}
          </button>
        )}
      </div>
      {!editing && (
        <>
          <p className={`text-xs ${gilt ? "" : "muted"}`} style={{ maxWidth: 640 }}>
            {gilt ? (
              <>
                <span className="mono">{gilt.system}</span>{" · "}
                <span className="mono">{gilt.project}</span>
                {!eigenes && <span className="muted">{" — " + t("org.repo.isDefault")}</span>}
              </>
            ) : (
              t(aus ? "org.repo.stateOff" : "org.repo.empty")
            )}
          </p>
          {gilt && doctor && (
            <RepoZugang
              doctor={doctor}
              system={gilt.system}
              systeme={systeme.data}
              canFile={own.data.platform_repo_can_file === true}
            />
          )}
        </>
      )}
      {editing && (
        <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} style={{ maxWidth: 640 }}>
          <div className="flex gap-2 items-end flex-wrap">
            <div>
              <label>{t("org.repo.system")}</label>
              <select value={system} onChange={(e) => setSystem(e.target.value)}>
                {/* The default stands as the first choice and by name —
                    "— none —" described a state that does not exist
                    anymore. */}
                <option value="">
                  {build.data?.source_project
                    ? t("org.repo.defaultOption", { project: build.data.source_project })
                    : t("org.repo.none")}
                </option>
                {wahl.map((x) => (
                  <option key={x.name} value={x.name}>{x.label || x.name}</option>
                ))}
                <option value={REPO_AUS}>{t("org.repo.offOption")}</option>
              </select>
            </div>
            {/* A project belongs only to a chosen target system: the default
                brings its own, "off" needs none. */}
            {system !== "" && system !== REPO_AUS && (
              <div className="flex-1 min-w-52">
                <label>{t("org.repo.project")}</label>
                <input
                  className="mono"
                  value={project}
                  onChange={(e) => setProject(e.target.value)}
                  placeholder={t("org.repo.projectPlaceholder")}
                />
              </div>
            )}
          </div>
          <div className="muted text-xs" style={{ margin: "3px 0 6px" }}>{t("org.repo.hint")}</div>
          {error && <div className="danger-text text-xs mb-2">{error}</div>}
          <div className="flex gap-2">
            <button className="btn sm primary" type="submit" disabled={save.isPending}>
              {t("org.company.save")}
            </button>
            <button className="btn sm" type="button" onClick={() => { setEditing(false); setError(""); }}>
              {t("modal.cancel")}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}

export default function Org() {
  const { t } = useTranslation();
  const chart = useQuery({
    queryKey: ["orgchart"],
    queryFn: () => api<OrgChart>("/org/chart"),
  });
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });

  if (chart.isError) return <p className="danger-text">{t("org.loadError")}</p>;
  /* Not isLoading: that is only the FIRST load. Between a failed attempt and
     the retry the query stands on "pending, but not on the road right now" —
     isLoading false, isError still false, data undefined. The exclamation
     mark behind it took the page apart in exactly this moment: a 401 on
     /org/chart (expired session), and instead of the login came a white
     page, because React throws the tree away on the exception. Check the
     data, not a state that merely brings them
     along most of the time. */
  if (!chart.data) return null;

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("org.title")}</h1>
        <span className="muted">{t("org.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("org.desc")}
      </p>

      <OrgChartView chart={chart.data} orgName={own.data?.name ?? ""} head={<CompanyDescription variant="head" />} />
    </div>
  );
}
