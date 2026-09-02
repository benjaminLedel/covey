import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api, patch, buildInfo,
  type Agent, type AgentSystem, type OrgChart, type Organization,
} from "../api";
import { OrgChart as OrgChartView } from "../components/orgchart/OrgChart";

export function CompanyDescription() {
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
    <div className="card mb-4">
      <div className="flex items-baseline gap-2 mb-1">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>{own.data.name}</h2>
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

/* Wo der Quelltext dieser Plattform liegt (spec/21).
 *
 * covey Doctor macht den wertvollsten Befund dort, wo er ihn nicht
 * durch eine Config beheben kann — und er ist der Einzige in der Organisation,
 * der ihn bei mehreren Kollegen gleichzeitig gesehen hat. Damit daraus eine
 * Diagnose statt eines Symptoms wird, liest er den Quelltext; damit der Bericht
 * ankommt, meldet er ins selbe Repository.
 *
 * WELCHES, entscheidet die Organisation. Eine Instanz gegen den öffentlichen
 * GitHub-Spiegel hätte sonst einen Agenten, der Issues dorthin schreibt, wo die
 * Welt mitliest. Deshalb steht das hier bei den Stammdaten und nicht in einem
 * Prompt. Leer heißt: die dritte Schicht gibt es nicht, und im Prompt steht
 * davon auch nichts.
 *
 * Zwei Dinge haben der Karte gefehlt, und beide machten sie zu einer
 * Einstellung, die man dort nicht erwartet, wo sie steht:
 *
 * Am Organigramm stand sie auch auf Instanzen ohne covey Doctor — ein Formular
 * für einen Agenten, den es nicht gibt, zwischen den Menschen und Abteilungen,
 * die es gibt. Dort hängt sie jetzt an ihm (nurMitDoctor): Sie ist da
 * Zusammenhang zu einem Kollegen, den man im Chart sieht. In den Stammdaten der
 * Verwaltung bleibt sie immer stehen — das ist die Fläche für Einstellungen,
 * auch für die noch ungenutzten.
 *
 * Und sie war die halbe Einrichtung: ohne eine Zeile in der ACCESS.md von covey
 * Doctor bleibt der Abschnitt aus seinem Prompt, und davon stand nichts auf der
 * Karte, sondern im Kleingedruckten des Formulars. Wer speicherte, sah nicht,
 * ob es gewirkt hat. Jetzt steht der Zustand da, wo das Ergebnis steht
 * (RepoZugang). */

/* Ob die Einstellung überhaupt wirkt — gelesen aus derselben Quelle, aus der
   der Prompt entsteht: `access` ist die Zeile in der ACCESS.md von covey
   Doctor, `enabled` die Freigabe des Plugins für die Organisation. */
function RepoZugang({
  doctor,
  system,
  systeme,
}: {
  doctor: Agent;
  system: string;
  systeme?: AgentSystem[] | null;
}) {
  const { t } = useTranslation();
  if (!systeme) return null; // noch nicht geladen — lieber nichts als eine Vermutung
  const eintrag = systeme.find((s) => s.name === system);
  const zumAgenten = (
    <Link to={`/agents/${doctor.id}?tab=config`}>{doctor.display_name}</Link>
  );

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

/* „Aus" — dasselbe Zeichen wie agents.RepoOff im Backend. Es brauchte einen
   eigenen Wert, seit es die Voreinstellung gibt: „leer" hieß früher „gar
   nicht" und heißt jetzt „das Projekt, aus dem dieses Programm stammt". */
const REPO_AUS = "-";

// Die Aufzeichnung: wie tief mitgeschrieben wird, und wie lange der wörtliche
// Verlauf bleibt (spec/06). Beides org-weit, beides am Agenten überschreibbar —
// die Tiefe nur nach oben, die Frist nur nach länger. Ein Agent, der seine
// eigene Spur kürzen könnte, wäre genau die Lücke, die eine org-weite
// Einstellung schließen soll.
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

export function PlatformRepo({ nurMitDoctor = false }: { nurMitDoctor?: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const own = useQuery({ queryKey: ["own-org"], queryFn: () => api<Organization>("/org") });
  /* Woher dieses Programm kommt — die Voreinstellung. Sie steht nicht in der
     Oberfläche, sondern kommt vom Server (buildinfo), damit ein Fork sein
     eigenes Projekt trägt und nicht das des Ursprungs. */
  const build = useQuery({ queryKey: ["build"], queryFn: buildInfo, staleTime: Infinity });
  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<{ name: string; label: string; enabled: boolean }[] | null>("/targets"),
  });
  /* Derselbe Schlüssel wie auf der Seite ringsum: die Abfrage läuft nicht ein
     zweites Mal, die Karte liest nur mit. */
  const chart = useQuery({ queryKey: ["orgchart"], queryFn: () => api<OrgChart>("/org/chart") });
  const doctor = (chart.data?.agents ?? []).find((a) => a.slug === "covey-doctor");
  /* Was covey Doctor an Zugängen HAT — dieselbe Quelle, aus der sein Prompt
     entsteht (access = eine Zeile in seiner ACCESS.md). */
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
    /* Voreinstellung und „aus" tragen kein Projekt — was im Feld stand, bevor
       jemand das Zielsystem gewechselt hat, wird nicht mitgespeichert. */
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

  /* Am Organigramm ohne covey Doctor: keine Karte. Wer ihn einstellt, findet
     sie mit ihm vor — in den Stammdaten steht sie ohnehin. */
  if (!own.data || (nurMitDoctor && !doctor)) return null;

  /* Dieselbe Auflösung wie im Backend (agents.PlatformRepo): eigenes
     Repository, sonst das Projekt dieser Plattform, außer es ist abgeschaltet.
     Zwei Stellen, eine Regel — die Karte soll zeigen, was gilt, nicht was
     gespeichert ist. */
  const eigenes = own.data.platform_repo_system && own.data.platform_repo_project;
  const aus = own.data.platform_repo_system === REPO_AUS;
  const gilt = aus
    ? null
    : eigenes
      ? { system: own.data.platform_repo_system!, project: own.data.platform_repo_project! }
      : build.data?.source_system && build.data.source_project
        ? { system: build.data.source_system, project: build.data.source_project }
        : null;

  // Nur angeschlossene Zielsysteme: eine Adresse auf einem System ohne
  // Credential liefe erst beim Checkout ins Leere.
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
            <RepoZugang doctor={doctor} system={gilt.system} systeme={systeme.data} />
          )}
        </>
      )}
      {editing && (
        <form onSubmit={(e) => { e.preventDefault(); save.mutate(); }} style={{ maxWidth: 640 }}>
          <div className="flex gap-2 items-end flex-wrap">
            <div>
              <label>{t("org.repo.system")}</label>
              <select value={system} onChange={(e) => setSystem(e.target.value)}>
                {/* Die Voreinstellung steht als erste Wahl und mit Namen da —
                    „— keines —" beschrieb einen Zustand, den es nicht mehr
                    gibt. */}
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
            {/* Ein Projekt gehört nur zu einem selbst gewählten Zielsystem: die
                Voreinstellung bringt ihres mit, „aus" braucht keins. */}
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
  /* Nicht isLoading: das ist nur der ERSTE Ladevorgang. Zwischen einem
     fehlgeschlagenen Versuch und dem Wiederholungsversuch steht die Abfrage auf
     „pending, aber gerade nicht unterwegs" — isLoading false, isError noch
     false, data undefined. Das Ausrufezeichen dahinter hat die Seite in genau
     diesem Moment zerlegt: eine 401 auf /org/chart (abgelaufene Sitzung), und
     statt der Anmeldung kam eine weisse Seite, weil React den Baum bei der
     Ausnahme abwirft. Auf die Daten prüfen, nicht auf einen Zustand, der sie
     bloß meistens mitbringt. */
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

      <CompanyDescription />
      <PlatformRepo nurMitDoctor />

      <OrgChartView chart={chart.data} orgName={own.data?.name ?? ""} />
    </div>
  );
}
