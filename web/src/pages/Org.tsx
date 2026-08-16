import { useEffect, useRef, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api, patch, isDraft, buildInfo,
  createDepartment, renameDepartment, deleteDepartment, setDepartmentColor,
  setAgentDepartment, setAgentSupervisor,
  setHumanDepartment, setHumanManager,
  addDepartmentLead, removeDepartmentLead,
  type Agent, type AgentSystem, type Human, type OrgChart, type Department, type DeptLead,
  type Organization,
} from "../api";
import { Avatar } from "../components/person";
import { ConfirmDialog } from "../components/Modal";

// Ziel eines Drop-Vorgangs: eine Abteilung (dann direktes Mitglied) oder ein
// Mitglied (dann Unterstellung an dieses Mitglied, in dessen Abteilung).
type DropTarget =
  | { deptId: string | null; supervisorId: null }        // direktes Mitglied einer Abteilung
  | { deptId: string | null; supervisorId: string };     // Untergebener eines Mitglieds

// Vorgegebene Akzentfarben für Abteilungen, abgestimmt auf das Papier-Theme.
// Leer = Standard-Akzent (var(--text-accent)).
const DEPT_COLORS = [
  "#7a83cc", "#b25f41", "#7d9471", "#c9a227",
  "#5e9b94", "#9a6b8f", "#6b87a8", "#8a8577",
];

// Farbwahl als Swatch-Reihe: erster Punkt = Standard, danach die Palette.
function ColorSwatches({ value, onPick }: { value: string; onPick: (c: string) => void }) {
  const { t } = useTranslation();
  return (
    <div className="dept-colors" role="radiogroup" aria-label={t("org.deptColor")}>
      <button
        type="button"
        className={`dept-swatch none${value === "" ? " sel" : ""}`}
        onClick={() => onPick("")}
        title={t("org.colorDefault")}
      />
      {DEPT_COLORS.map(c => (
        <button
          type="button"
          key={c}
          className={`dept-swatch${value === c ? " sel" : ""}`}
          style={{ background: c }}
          onClick={() => onPick(c)}
          title={c}
        />
      ))}
    </div>
  );
}

// Gezogen werden Agenten und Menschen. Der Typ entscheidet über die API-Aufrufe
// und die erlaubten Ziele: Menschen können nur Menschen unterstellt werden
// (manager_id verweist auf humans), Agenten Menschen wie Agenten.
type DragItem =
  | { kind: "agent"; member: Agent }
  | { kind: "human"; member: Human };

/* Was dieses Unternehmen macht — Stammdaten der Organisation, nicht Deko.
 *
 * Derselbe Absatz beantwortet dieselbe Frage an mehreren Stellen: in der
 * Config neu entworfener Agenten, in jeder Ausschreibung an die
 * Personalabteilung und im Systemprompt des Config-Assistenten. Deshalb steht
 * er hier, wo man ihn beim Lesen des Org-Charts sowieso vor Augen hat — und
 * nicht in der Mandantenverwaltung, in die die meisten nie kommen. */
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
 * Covey Doctor macht den wertvollsten Befund dort, wo er ihn nicht
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
 * Am Organigramm stand sie auch auf Instanzen ohne Covey Doctor — ein Formular
 * für einen Agenten, den es nicht gibt, zwischen den Menschen und Abteilungen,
 * die es gibt. Dort hängt sie jetzt an ihm (nurMitDoctor): Sie ist da
 * Zusammenhang zu einem Kollegen, den man im Chart sieht. In den Stammdaten der
 * Verwaltung bleibt sie immer stehen — das ist die Fläche für Einstellungen,
 * auch für die noch ungenutzten.
 *
 * Und sie war die halbe Einrichtung: ohne eine Zeile in der ACCESS.md von Covey
 * Doctor bleibt der Abschnitt aus seinem Prompt, und davon stand nichts auf der
 * Karte, sondern im Kleingedruckten des Formulars. Wer speicherte, sah nicht,
 * ob es gewirkt hat. Jetzt steht der Zustand da, wo das Ergebnis steht
 * (RepoZugang). */

/* Ob die Einstellung überhaupt wirkt — gelesen aus derselben Quelle, aus der
   der Prompt entsteht: `access` ist die Zeile in der ACCESS.md von Covey
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
  /* Was Covey Doctor an Zugängen HAT — dieselbe Quelle, aus der sein Prompt
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

  /* Am Organigramm ohne Covey Doctor: keine Karte. Wer ihn einstellt, findet
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
  const qc = useQueryClient();
  const [dragging, setDragging] = useState<DragItem | null>(null);
  const [showNewDept, setShowNewDept] = useState(false);
  const [newName, setNewName] = useState("");
  const [newDesc, setNewDesc] = useState("");
  const [newColor, setNewColor] = useState("");

  const chart = useQuery({
    queryKey: ["orgchart"],
    queryFn: () => api<OrgChart>("/org/chart"),
  });

  const createMut = useMutation({
    mutationFn: ({ name, desc, color }: { name: string; desc: string; color: string }) =>
      createDepartment(name, desc, color),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["orgchart"] });
      setNewName("");
      setNewDesc("");
      setNewColor("");
      setShowNewDept(false);
    },
  });

  // Ein Drop setzt Vorgesetzten und Abteilung gemeinsam, damit ein
  // untergeordnetes Mitglied in derselben Abteilung wie sein Vorgesetzter landet.
  const moveMut = useMutation({
    mutationFn: async ({ item, deptId, supervisorId }: { item: DragItem } & DropTarget) => {
      if (item.kind === "agent") {
        await setAgentSupervisor(item.member.id, supervisorId);
        await setAgentDepartment(item.member.id, deptId);
      } else {
        await setHumanManager(item.member.id, supervisorId);
        await setHumanDepartment(item.member.id, deptId);
      }
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["orgchart"] }),
  });

  const drop = (target: DropTarget) => {
    if (dragging) moveMut.mutate({ item: dragging, ...target });
    setDragging(null);
  };

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
  const { humans, agents, departments } = chart.data;

  const invalidate = () => qc.invalidateQueries({ queryKey: ["orgchart"] });

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

      {/* Legende + Aktionsleiste */}
      <div className="org-legend">
        <span>
          <span className="sw" style={{ background: "var(--text-secondary)" }} />
          {t("org.legendHuman")}
        </span>
        <span>
          <span className="sw" style={{ background: "var(--text-accent)" }} />
          {t("org.legendAgent")}
        </span>
        <span className="muted">{t("org.legendHint")}</span>
        <button className="btn sm" style={{ marginLeft: "auto" }} onClick={() => setShowNewDept(v => !v)}>
          {t("org.newDept")}
        </button>
      </div>

      {/* Formular: neue Abteilung */}
      {showNewDept && (
        <form
          className="dept-create-form"
          onSubmit={e => {
            e.preventDefault();
            createMut.mutate({ name: newName, desc: newDesc, color: newColor });
          }}
        >
          <input
            value={newName}
            onChange={e => setNewName(e.target.value)}
            placeholder={t("org.deptNamePlaceholder")}
            required
            autoFocus
            style={{ flex: "1 1 160px", minWidth: 0 }}
          />
          <input
            value={newDesc}
            onChange={e => setNewDesc(e.target.value)}
            placeholder={t("org.deptDescPlaceholder")}
            style={{ flex: "2 1 220px", minWidth: 0 }}
          />
          <ColorSwatches value={newColor} onPick={setNewColor} />
          <button className="btn sm primary" type="submit" disabled={createMut.isPending}>
            {t("org.createDept")}
          </button>
          <button type="button" className="btn sm" onClick={() => setShowNewDept(false)}>
            {t("modal.cancel")}
          </button>
        </form>
      )}

      <DiagramView
        humans={humans}
        agents={agents}
        departments={departments}
        dragging={dragging}
        onDragStart={setDragging}
        onDragEnd={() => setDragging(null)}
        onDrop={drop}
        onUpdate={invalidate}
      />
    </div>
  );
}

// Zwei Dinge, die erst der Browser weiß, und die der Baum trotzdem braucht:
//
//  1. Den Breiten-Deckel der umbrechenden Ebenen — in Pixeln, denn Prozente
//     helfen nicht: alle Vorfahren im Baum sind max-content-breit. Der Hook
//     schreibt die sichtbare Breite des Scroll-Containers als --tree-avail.
//  2. Wo die Reihen einer umgebrochenen Ebene anfangen und aufhören. Erst
//     danach lassen sich die Verbinder zeichnen (data-row-first/-last, siehe
//     .tree-grid in styles.css) und die senkrechte Schiene bemessen, die die
//     Reihen zusammenhält (--grid-spine).
//
// Beide Male werden nur data-Attribute und CSS-Variablen gesetzt, die auf
// absolut positionierte Pseudo-Elemente wirken — das löst kein neues Layout
// aus, der ResizeObserver läuft also nicht im Kreis.
// Der Stamm einer umgebrochenen Ebene: die Senkrechte, an der die Reihen
// hängen. Sie soll unter dem Elternknoten herunterlaufen — also möglichst
// mittig — darf dabei aber keine Karte kreuzen. Deshalb sucht placeTrunk die
// freie Spalte (Lücke zwischen zwei Karten, in ALLEN gekreuzten Reihen frei),
// die der Mitte am nächsten liegt; notfalls landet der Stamm links außen.
// Wo eine Reihe den Stamm nicht von selbst erreicht, bekommt ihre erste bzw.
// letzte Karte einen Arm dorthin (data-arm-left/-right).
function placeTrunk(ul: HTMLElement, items: HTMLElement[]) {
  const ulLeft = ul.getBoundingClientRect().left;
  const rows: HTMLElement[][] = [];
  for (const li of items) {
    const row = rows[rows.length - 1];
    if (row && row[0].offsetTop === li.offsetTop) row.push(li);
    else rows.push([li]);
  }
  // Die Karte sitzt mit Innenabstand im li — die tatsächlich freie Lücke ist
  // breiter als der Abstand der li-Kästen.
  const box = (li: HTMLElement) => {
    const r = (li.firstElementChild ?? li).getBoundingClientRect();
    return { l: r.left - ulLeft, r: r.right - ulLeft };
  };
  // Freie Spalten = Schnittmenge der Lücken aller Reihen, die der Stamm
  // durchquert (die letzte kreuzt er nicht, dort endet er).
  let free = [{ l: -1e6, r: 1e6 }];
  for (const row of rows.slice(0, -1)) {
    const boxes = row.map(box);
    const gaps = [{ l: -1e6, r: boxes[0].l }];
    for (let i = 1; i < boxes.length; i++) gaps.push({ l: boxes[i - 1].r, r: boxes[i].l });
    const next: typeof free = [];
    for (const a of free) for (const b of gaps) {
      const l = Math.max(a.l, b.l), r = Math.min(a.r, b.r);
      if (r - l >= 8) next.push({ l, r });
    }
    free = next;
  }
  const center = ul.getBoundingClientRect().width / 2;
  let trunk = -14;
  let best = Infinity;
  for (const g of free) {
    const x = Math.min(Math.max(center, g.l + 4), g.r - 4);
    if (Math.abs(x - center) < best) { best = Math.abs(x - center); trunk = x; }
  }
  ul.style.setProperty("--trunk-x", `${Math.round(trunk)}px`);

  for (const row of rows) {
    const first = row[0], last = row[row.length - 1];
    const firstMid = first.offsetLeft + first.offsetWidth / 2;
    const lastMid = last.offsetLeft + last.offsetWidth / 2;
    const armLeft = trunk < firstMid - 1;
    const armRight = trunk > lastMid + 1;
    first.toggleAttribute("data-arm-left", armLeft);
    first.style.setProperty("--arm", `${Math.round(trunk - first.offsetLeft)}px`);
    last.toggleAttribute("data-arm-right", armRight);
    last.style.setProperty("--arm-right", `${Math.round(last.offsetLeft + last.offsetWidth - trunk)}px`);
    // Mit Arm nach rechts zeichnet die letzte Karte den Balken selbst weiter —
    // dann darf die Ecke der Reihen-Zeichnung nicht zusätzlich greifen.
    last.toggleAttribute("data-row-last", !armRight);
  }
}

// paused = es wird gerade gezogen. Dann bleibt das Layout, wie es ist: sonst
// vermisst der Hook mitten im Drag neu, die Gruppen brechen anders um und die
// Karte, auf die man zielt, ist beim Loslassen woanders.
function useTreeLayout(paused: boolean) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const root = ref.current;
    if (!root || paused) return;
    const measure = () => {
      root.style.setProperty("--tree-avail", `${root.clientWidth - 12}px`);
      // Von innen nach außen (Dokumentreihenfolge rückwärts): eine Abteilung
      // ist so breit wie ihr Mitglieder-Block. Erst wenn der auf seine Reihen
      // geschrumpft ist, stimmt die Breite, mit der die Abteilungs-Ebene
      // rechnet — sonst stapelt sie Karten, die nebeneinander passen.
      for (const ul of [...root.querySelectorAll<HTMLElement>("ul.tree-grid")].reverse()) {
        // Erst frei messen: eine zuvor gesetzte Breite würde den Umbruch
        // vorgeben, den wir gerade ermitteln wollen.
        ul.style.width = "";
        const items = [...ul.children] as HTMLElement[];
        if (items.length === 0) continue;
        // offsetTop zählt ab der ul (die ist position: relative) und trägt
        // deren Innenabstand — die erste Reihe liegt also nicht bei 0.
        const firstRow = items[0].offsetTop;
        const ulLeft = ul.getBoundingClientRect().left;
        let lastRow = firstRow;
        let widest = 0;
        items.forEach((li, i) => {
          const top = li.offsetTop;
          li.toggleAttribute("data-row-first", i === 0 || items[i - 1].offsetTop !== top);
          li.toggleAttribute("data-row-last", i === items.length - 1 || items[i + 1].offsetTop !== top);
          lastRow = Math.max(lastRow, top);
          // Auf Bruchteile genau: offsetWidth rundet ab, und schon ein halbes
          // Pixel zu wenig wirft die letzte Karte der Reihe in die nächste.
          widest = Math.max(widest, li.getBoundingClientRect().right - ulLeft);
        });
        // Die Schiene reicht vom Balken der ersten bis zu dem der letzten
        // Reihe. Passt die Ebene doch in eine Reihe, bleibt es bei der
        // klassischen Zeichnung — data-wrapped schaltet Schiene und den Arm
        // zu ihr wieder ab.
        const wrapped = lastRow > firstRow;
        ul.toggleAttribute("data-wrapped", wrapped);
        ul.style.setProperty("--grid-spine", `${lastRow - firstRow}px`);
        // Auf die breiteste Reihe schrumpfen. Sonst bleibt die Gruppe so breit
        // wie ihr Deckel, der Elternknoten sitzt über deren Mitte — und damit
        // sichtbar neben seinen Kindern. Die Reihen sind linksbündig, der
        // Umbruch ändert sich durch die Breite also nicht.
        if (wrapped) ul.style.width = `${Math.ceil(widest) + 1}px`;
        if (wrapped) placeTrunk(ul, items);
      }
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(root);
    for (const ul of root.querySelectorAll("ul.tree-grid")) ro.observe(ul);
    return () => ro.disconnect();
  });
  return ref;
}

// Ab dieser Gruppengröße startet ein Knoten zugeklappt — sonst wird eine
// Organisation mit ein paar Dutzend Leuten zur Tapete. Wer auf- oder zuklappt,
// überschreibt das dauerhaft: die Wahl steht je Knoten im localStorage, der
// Vorgabewert gilt nur, solange nichts dort steht.
const AUTO_COLLAPSE_AT = 8;
const COLLAPSE_KEY = "covey.org.collapsed";

function useCollapse() {
  const [choice, setChoice] = useState<Record<string, boolean>>(() => {
    try {
      return JSON.parse(localStorage.getItem(COLLAPSE_KEY) || "{}") as Record<string, boolean>;
    } catch {
      return {};
    }
  });
  const isOpen = (id: string, size: number) => choice[id] ?? size < AUTO_COLLAPSE_AT;
  const toggle = (id: string, size: number) =>
    setChoice(prev => {
      const next = { ...prev, [id]: !(prev[id] ?? size < AUTO_COLLAPSE_AT) };
      localStorage.setItem(COLLAPSE_KEY, JSON.stringify(next));
      return next;
    });
  return { isOpen, toggle };
}
type Collapse = ReturnType<typeof useCollapse>;

// Aufklapper: zeigt zugeklappt, wie viele Knoten darunter verschwunden sind.
// An der Abteilungs-Karte bleibt die Zahl weg — dort steht sie schon als
// „N Mitglieder" unter dem Namen.
function CollapseToggle({ open, count, onToggle, className = "subtree-toggle", showCount = true }: {
  open: boolean; count: number; onToggle: () => void; className?: string; showCount?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <button
      className={`${className}${open ? "" : " closed"}`}
      draggable={false}
      title={open ? t("org.collapse") : t("org.expand", { count })}
      aria-expanded={open}
      onClick={e => { e.preventDefault(); e.stopPropagation(); onToggle(); }}
      onMouseDown={e => e.stopPropagation()}
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
        <path d="M6 9l6 6 6-6" />
      </svg>
      {!open && showCount && <span className="n">{count}</span>}
    </button>
  );
}

// Zahl der Unterstellten unterhalb eines Mitglieds (transitiv, innerhalb der
// Abteilung). Sie steht am Aufklapper, wenn der Zweig zu ist.
function countBelow(members: Members, id: string, seen: Set<string>): number {
  const kids: { id: string }[] = [
    ...members.humans.filter(h => h.manager_id === id && !seen.has(h.id)),
    ...members.agents.filter(a => a.supervisor_id === id && !seen.has(a.id)),
  ];
  return kids.reduce((n, k) => n + 1 + countBelow(members, k.id, new Set(seen).add(k.id)), 0);
}

/* ── Diagramm: Organisation → Abteilungen → Berichtsbaum ───────────────
   Innerhalb einer Abteilung wird die Vorgesetzten-Hierarchie abgebildet
   (Menschen via manager_id, Agenten via supervisor_id). Agenten lassen sich
   per Drag & Drop auf eine Abteilung (→ direktes Mitglied) oder auf ein
   Mitglied (→ dessen Untergebener) ziehen. */
function DiagramView({
  humans, agents, departments, dragging, onDragStart, onDragEnd, onDrop, onUpdate,
}: {
  humans: Human[];
  agents: Agent[];
  departments: Department[];
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  onUpdate: () => void;
}) {
  const { t } = useTranslation();
  // Hooks vor jeden frühen Ausstieg: sonst ändert sich ihre Zahl, sobald die
  // erste Abteilung angelegt wird, und React bricht ab.
  const collapse = useCollapse();
  const treeRef = useTreeLayout(dragging !== null);

  const inDept = (deptId: string | null) => ({
    humans: humans.filter(h => (h.department_id ?? null) === deptId),
    agents: agents.filter(a => (a.department_id ?? null) === deptId),
  });

  const unassigned = inDept(null);
  const hasUnassigned = unassigned.humans.length + unassigned.agents.length > 0;

  if (departments.length === 0 && !hasUnassigned) {
    return <p className="muted mt-4">{t("org.noDepts")}</p>;
  }

  const memberHandlers = { dragging, onDragStart, onDragEnd, onDrop, collapse };

  // Leitungen sind org-weit referenziert — eine Leitung muss nicht Mitglied
  // ihrer Abteilung sein, daher gegen die vollen Listen auflösen.
  const resolveLead = (l: DeptLead): Human | Agent | undefined =>
    l.kind === "human" ? humans.find(h => h.id === l.id) : agents.find(a => a.id === l.id);

  return (
    <div className="tree mt-4" ref={treeRef}>
      <ul>
        <li>
          <div className="node org">
            <div className="nm">{t("org.rootOrg")}</div>
            <div className="rl">{t("org.rootOrgSub", { count: humans.length + agents.length })}</div>
          </div>
          <ul className={departments.length + (hasUnassigned ? 1 : 0) > WRAP_DEPTS_AT ? "tree-grid tree-grid-wide" : undefined}>
            {departments.map(dept => (
              <DeptTreeNode
                key={dept.id}
                dept={dept}
                members={inDept(dept.id)}
                resolveLead={resolveLead}
                {...memberHandlers}
                onUpdate={onUpdate}
              />
            ))}
            {hasUnassigned && (
              <UnassignedTreeNode members={unassigned} {...memberHandlers} />
            )}
          </ul>
        </li>
      </ul>
    </div>
  );
}

type Members = { humans: Human[]; agents: Agent[] };

// Ab wie vielen Geschwistern eine Ebene in mehrere Reihen umbricht. Eine
// Flex-Zeile pro Ebene lässt den Baum sonst endlos nach rechts wachsen — zehn
// Mitarbeiter einer Abteilung sind gut zwei Bildschirmbreiten. Umgebrochene
// Ebenen tragen ihre Zugehörigkeit über eine Klammer statt über T-Verbinder
// (siehe .tree-grid in styles.css): eine CSS-Linie kann Reihen nicht sauber
// verbinden, weil das Layout erst im Browser entscheidet, wo umgebrochen wird.
const WRAP_AT = 3;       // Mitglieder je Ebene
const WRAP_DEPTS_AT = 1; // Abteilungen: schon ab zwei Karten, die sind breit

// roots einer Abteilung = Mitglieder, deren Vorgesetzter nicht in derselben
// Abteilung sitzt (oder keiner). Kinder werden nur innerhalb der Abteilung
// aufgelöst.
function rootsOf(members: Members) {
  const humanIds = new Set(members.humans.map(h => h.id));
  const agentIds = new Set(members.agents.map(a => a.id));
  const parentInside = (pid?: string) => !!pid && (humanIds.has(pid) || agentIds.has(pid));
  return {
    humans: members.humans.filter(h => !parentInside(h.manager_id)),
    agents: members.agents.filter(a => !parentInside(a.supervisor_id)),
  };
}

function MemberBranch({
  members, parentId, seen, leadIds, onRemoveLead, dragging, onDragStart, onDragEnd, onDrop, collapse,
}: {
  members: Members;
  parentId?: string;      // undefined = roots der Abteilung
  seen: Set<string>;
  leadIds: Set<string>;   // Mitglieder, die Leitung der Abteilung sind
  onRemoveLead: (memberId: string) => void;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  collapse: Collapse;
}) {
  // Auf Wurzel-Ebene stehen die Leitungen zuerst — sie sind die Spitze der
  // Abteilung, alles andere hängt darunter. Stabile Sortierung erhält sonst
  // die Reihenfolge.
  const leadFirst = <T extends { id: string }>(list: T[]) =>
    parentId === undefined
      ? [...list].sort((a, b) => Number(leadIds.has(b.id)) - Number(leadIds.has(a.id)))
      : list;
  const childHumans = leadFirst(parentId === undefined
    ? rootsOf(members).humans
    : members.humans.filter(h => h.manager_id === parentId && !seen.has(h.id)));
  const childAgents = leadFirst(parentId === undefined
    ? rootsOf(members).agents
    : members.agents.filter(a => a.supervisor_id === parentId && !seen.has(a.id)));

  const count = childHumans.length + childAgents.length;
  if (count === 0) return null;

  return (
    <ul className={count > WRAP_AT ? "tree-grid" : undefined}>
      {childHumans.map(h => (
        <MemberNode
          key={h.id}
          member={h}
          kind="human"
          members={members}
          seen={seen}
          leadIds={leadIds}
          onRemoveLead={onRemoveLead}
          dragging={dragging}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
          collapse={collapse}
        />
      ))}
      {childAgents.map(a => (
        <MemberNode
          key={a.id}
          member={a}
          kind="agent"
          members={members}
          seen={seen}
          leadIds={leadIds}
          onRemoveLead={onRemoveLead}
          dragging={dragging}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
          collapse={collapse}
        />
      ))}
    </ul>
  );
}

function MemberNode({
  member, kind, members, seen, leadIds, onRemoveLead, dragging, onDragStart, onDragEnd, onDrop, collapse,
}: {
  member: Human | Agent;
  kind: "human" | "agent";
  members: Members;
  seen: Set<string>;
  leadIds: Set<string>;
  onRemoveLead: (memberId: string) => void;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  collapse: Collapse;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const isAgent = kind === "agent";
  const agent = isAgent ? (member as Agent) : null;
  const human = !isAgent ? (member as Human) : null;
  const isLead = leadIds.has(member.id);

  const beingDragged = dragging?.member.id === member.id;
  // Menschen können nur Menschen unterstellt werden (manager_id → humans);
  // Agenten dürfen auf beides fallen.
  const canDrop = !!dragging && dragging.member.id !== member.id
    && (dragging.kind === "agent" || !isAgent);
  const draft = agent ? isDraft(agent) : false;
  const status = agent ? (agent.killed ? "killed" : agent.status) : "";
  const nextSeen = new Set(seen).add(member.id);
  const deptId = (member.department_id ?? null) as string | null;

  // Eigener Zweig: aufklappbar, wenn jemand darunter hängt.
  const below = countBelow(members, member.id, nextSeen);
  const open = below === 0 || collapse.isOpen(member.id, below);

  return (
    <li>
      <div
        className={`orgmember${isLead ? " orgmember-lead" : ""}${beingDragged ? " orgmember-out" : ""}${isOver && canDrop ? " node-drop-over" : ""}`}
        draggable
        onDragStart={e => {
          e.dataTransfer.effectAllowed = "move";
          onDragStart(isAgent ? { kind: "agent", member: agent! } : { kind: "human", member: human! });
        }}
        onDragEnd={onDragEnd}
        onDragOver={e => { if (canDrop) { e.preventDefault(); e.stopPropagation(); setIsOver(true); } }}
        onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false); }}
        onDrop={e => { if (canDrop) { e.preventDefault(); e.stopPropagation(); setIsOver(false); onDrop({ deptId, supervisorId: member.id }); } }}
        title={canDrop ? t("org.dropOnMember") : undefined}
      >
        <span className="agent-grip" title={t("org.dragAgent")}>
          <svg viewBox="0 0 10 16" fill="currentColor">
            <circle cx="3" cy="3" r="1.2" /><circle cx="7" cy="3" r="1.2" />
            <circle cx="3" cy="8" r="1.2" /><circle cx="7" cy="8" r="1.2" />
            <circle cx="3" cy="13" r="1.2" /><circle cx="7" cy="13" r="1.2" />
          </svg>
        </span>
        <Link
          to={isAgent ? `/agents/${member.id}` : `/people/${member.id}`}
          className={`node ${kind}`}
          draggable={false}
          title={t("org.openProfile")}
        >
          <Avatar name={member.display_name} human={!isAgent} />
          <div>
            <div className="nm">{member.display_name}</div>
            <div className={`rl${isAgent && !agent!.job_title ? " mono" : ""}`}>
              {isAgent ? (agent!.job_title || agent!.slug) : (human!.job_title || t(`role.${human!.role}`, human!.role))}
            </div>
          </div>
          {isAgent ? (
            draft
              ? <span className="badge st-draft">{t("dashboard.draftBadge")}</span>
              : <span className={`badge st-${status}`}>{t(`status.${status}`, status)}</span>
          ) : (
            <span className="ntag">{t("org.nodeHuman")}</span>
          )}
        </Link>
        {isLead && (
          <span className="lead-pill" title={t("org.leadLabel")}>
            {t("org.leadLabel")}
            <button
              className="icon-btn danger"
              onClick={e => { e.preventDefault(); e.stopPropagation(); onRemoveLead(member.id); }}
              title={t("org.removeLead")}
            >✕</button>
          </span>
        )}
        {below > 0 && (
          <CollapseToggle open={open} count={below} onToggle={() => collapse.toggle(member.id, below)} />
        )}
      </div>
      {open && (
        <MemberBranch
          members={members}
          parentId={member.id}
          seen={nextSeen}
          leadIds={leadIds}
          onRemoveLead={onRemoveLead}
          dragging={dragging}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
          collapse={collapse}
        />
      )}
    </li>
  );
}

function DeptTreeNode({
  dept, members, resolveLead, dragging, onDragStart, onDragEnd, onDrop, onUpdate, collapse,
}: {
  dept: Department;
  members: Members;
  resolveLead: (l: DeptLead) => Human | Agent | undefined;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  onUpdate: () => void;
  collapse: Collapse;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const [leadOver, setLeadOver] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [editName, setEditName] = useState(dept.name);
  const [confirmDelete, setConfirmDelete] = useState(false);

  const renameMut = useMutation({
    mutationFn: () => renameDepartment(dept.id, editName),
    onSuccess: () => { onUpdate(); setRenaming(false); },
  });
  const deleteMut = useMutation({
    mutationFn: () => deleteDepartment(dept.id),
    onSuccess: () => { onUpdate(); setConfirmDelete(false); },
  });
  const addLeadMut = useMutation({
    mutationFn: (item: DragItem) => addDepartmentLead(dept.id, item.kind, item.member.id),
    onSettled: onUpdate,
  });
  const removeLeadMut = useMutation({
    mutationFn: (memberId: string) => removeDepartmentLead(dept.id, memberId),
    onSettled: onUpdate,
  });
  const colorMut = useMutation({
    mutationFn: (color: string) => setDepartmentColor(dept.id, color),
    onSettled: onUpdate,
  });

  const total = members.humans.length + members.agents.length;
  const open = collapse.isOpen(dept.id, total);

  // Leitungen: interne (Mitglied dieser Abteilung) werden als Baum-Wurzel mit
  // „Leitung"-Badge gezeigt — daher kein doppelter Chip. Externe Leitungen
  // (kein Mitglied) haben keinen Baum-Knoten und bleiben als Chip im Kopf.
  const memberIds = new Set<string>([
    ...members.humans.map(h => h.id),
    ...members.agents.map(a => a.id),
  ]);
  const leadIds = new Set(dept.leads.map(l => l.id));
  const externalLeads = dept.leads.filter(l => !memberIds.has(l.id));

  // Akzentfarbe der Abteilung: Streifen + dezente Flächentönung. Inline, weil
  // die Farbe aus den Daten kommt; der Drop-Ring wird mitkomponiert, da die
  // Inline-Box-Shadow die der .node-drop-over-Klasse überdeckt.
  const accentStyle = dept.color ? {
    boxShadow: `inset 3px 0 0 ${dept.color}${isOver && dragging ? ", 0 0 0 2px rgba(var(--accent-rgb, 122,131,204),0.20)" : ""}`,
    background: `color-mix(in srgb, ${dept.color} 6%, var(--surface-2))`,
  } : undefined;

  return (
    <li>
      <div
        className={`node dept${isOver && dragging ? " node-drop-over" : ""}`}
        style={accentStyle}
        title={dept.description || undefined}
        onDragOver={e => { if (dragging) { e.preventDefault(); e.stopPropagation(); setIsOver(true); } }}
        onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false); }}
        onDrop={e => { e.preventDefault(); e.stopPropagation(); setIsOver(false); onDrop({ deptId: dept.id, supervisorId: null }); }}
      >
        {renaming ? (
          <>
            <form className="dept-tree-rename" onSubmit={e => { e.preventDefault(); renameMut.mutate(); }}>
              <input value={editName} onChange={e => setEditName(e.target.value)} autoFocus required />
              <button className="btn sm primary" type="submit" disabled={renameMut.isPending} style={{ padding: "3px 8px" }}>✓</button>
              <button type="button" className="btn sm" style={{ padding: "3px 8px" }} onClick={() => { setRenaming(false); setEditName(dept.name); }}>✕</button>
            </form>
            {/* Farbwahl wirkt sofort — kein eigener Speichern-Schritt nötig. */}
            <ColorSwatches value={dept.color} onPick={c => colorMut.mutate(c)} />
          </>
        ) : (
          <>
            <div className="dept-tree-hdr">
              {total > 0 && (
                <CollapseToggle
                  open={open}
                  count={total}
                  onToggle={() => collapse.toggle(dept.id, total)}
                  className="dept-toggle-btn"
                  showCount={false}
                />
              )}
              <span className="nm">{dept.name}</span>
              <button className="icon-btn" onClick={() => setRenaming(true)} title={t("org.renameDept")} style={{ fontSize: 12 }}>✎</button>
              <button className="icon-btn danger" onClick={() => setConfirmDelete(true)} title={t("org.deleteDept")} style={{ fontSize: 12 }}>✕</button>
            </div>
            <div className="rl">{total}&thinsp;{total === 1 ? t("org.member") : t("org.members")}</div>
          </>
        )}

        {/* Leitung: nur externe Leitungen als Chip — interne stehen als
            Baum-Wurzel mit „Leitung"-Badge unten. */}
        {externalLeads.length > 0 && (
          <div className="dept-leads">
            <span className="dept-leads-label">{t("org.leadLabel")}</span>
            {externalLeads.map(l => {
              const m = resolveLead(l);
              if (!m) return null;
              return (
                <span key={l.id} className="dept-lead-chip">
                  <Link
                    to={l.kind === "agent" ? `/agents/${l.id}` : `/people/${l.id}`}
                    draggable={false}
                    title={t("org.openProfile")}
                  >
                    <Avatar name={m.display_name} size={16} human={l.kind === "human"} />
                    {m.display_name}
                  </Link>
                  <button
                    className="icon-btn danger"
                    onClick={() => removeLeadMut.mutate(l.id)}
                    title={t("org.removeLead")}
                  >✕</button>
                </span>
              );
            })}
          </div>
        )}

        {/* Während eines Drag-Vorgangs: zwei großzügige Drop-Zonen. Mitglied
            setzt die Abteilung, Leitung lässt die Zugehörigkeit unberührt. */}
        {dragging && !renaming && (
          <div className="dept-dropzones">
            <div
              className={`dept-dz${isOver ? " over" : ""}`}
              onDragOver={e => { e.preventDefault(); e.stopPropagation(); setIsOver(true); setLeadOver(false); }}
              onDragLeave={() => setIsOver(false)}
              onDrop={e => { e.preventDefault(); e.stopPropagation(); setIsOver(false); onDrop({ deptId: dept.id, supervisorId: null }); }}
            >
              {t("org.dropAsMember")}
            </div>
            <div
              className={`dept-dz${leadOver ? " over" : ""}`}
              onDragOver={e => { e.preventDefault(); e.stopPropagation(); setLeadOver(true); setIsOver(false); }}
              onDragLeave={() => setLeadOver(false)}
              onDrop={e => { e.preventDefault(); e.stopPropagation(); setLeadOver(false); if (dragging) addLeadMut.mutate(dragging); }}
            >
              {t("org.dropAsLead")}
            </div>
          </div>
        )}
      </div>

      {confirmDelete && (
        <ConfirmDialog
          title={t("org.deleteDept")}
          confirmLabel={t("org.deleteDept")}
          onConfirm={() => deleteMut.mutate()}
          onClose={() => setConfirmDelete(false)}
          pending={deleteMut.isPending}
        >
          {t("org.deleteDeptConfirm", { name: dept.name })}
        </ConfirmDialog>
      )}

      {total > 0 ? (open && (
        <MemberBranch members={members} seen={new Set()} leadIds={leadIds} onRemoveLead={id => removeLeadMut.mutate(id)} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} onDrop={onDrop} collapse={collapse} />
      )) : (
        <ul>
          <li>
            <span className="dept-drop-hint">{dragging ? t("org.dropHere") : t("org.dragHint")}</span>
          </li>
        </ul>
      )}
    </li>
  );
}

function UnassignedTreeNode({
  members, dragging, onDragStart, onDragEnd, onDrop, collapse,
}: {
  members: Members;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  collapse: Collapse;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const total = members.humans.length + members.agents.length;
  const open = collapse.isOpen(UNASSIGNED_ID, total);

  return (
    <li>
      <div
        className={`node dept unassigned${isOver && dragging ? " node-drop-over" : ""}`}
        onDragOver={e => { if (dragging) { e.preventDefault(); e.stopPropagation(); setIsOver(true); } }}
        onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false); }}
        onDrop={e => { e.preventDefault(); e.stopPropagation(); setIsOver(false); onDrop({ deptId: null, supervisorId: null }); }}
      >
        <div className="dept-tree-hdr">
          {total > 0 && (
            <CollapseToggle
              open={open}
              count={total}
              onToggle={() => collapse.toggle(UNASSIGNED_ID, total)}
              className="dept-toggle-btn"
              showCount={false}
            />
          )}
          <span className="nm">{t("org.diagramUnassigned")}</span>
        </div>
        <div className="rl">{total}&thinsp;{total === 1 ? t("org.member") : t("org.members")}</div>
      </div>
      {total > 0 && open && (
        <MemberBranch members={members} seen={new Set()} leadIds={EMPTY_LEADS} onRemoveLead={NOOP} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} onDrop={onDrop} collapse={collapse} />
      )}
    </li>
  );
}

// „Ohne Abteilung" kennt keine Leitung — konstante Leerwerte, damit sich die
// Props nicht bei jedem Render neu bilden. Für den Aufklapper braucht der
// Pseudo-Knoten eine ID; sie kann mit keiner echten Abteilung kollidieren.
const UNASSIGNED_ID = "unassigned";
const EMPTY_LEADS = new Set<string>();
const NOOP = () => {};
