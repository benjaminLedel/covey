import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  api, roleLabel,
  createDepartment, renameDepartment, deleteDepartment, setDepartmentColor,
  setAgentDepartment, setAgentSupervisor,
  setHumanDepartment, setHumanManager,
  addDepartmentLead, removeDepartmentLead,
  type Agent, type Human, type OrgChart, type Department, type DeptLead,
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

  if (chart.isLoading) return null;
  if (chart.isError) return <p className="danger-text">{t("org.loadError")}</p>;
  const { humans, agents, departments } = chart.data!;

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

  const inDept = (deptId: string | null) => ({
    humans: humans.filter(h => (h.department_id ?? null) === deptId),
    agents: agents.filter(a => (a.department_id ?? null) === deptId),
  });

  const unassigned = inDept(null);
  const hasUnassigned = unassigned.humans.length + unassigned.agents.length > 0;

  if (departments.length === 0 && !hasUnassigned) {
    return <p className="muted mt-4">{t("org.noDepts")}</p>;
  }

  const memberHandlers = { dragging, onDragStart, onDragEnd, onDrop };

  // Leitungen sind org-weit referenziert — eine Leitung muss nicht Mitglied
  // ihrer Abteilung sein, daher gegen die vollen Listen auflösen.
  const resolveLead = (l: DeptLead): Human | Agent | undefined =>
    l.kind === "human" ? humans.find(h => h.id === l.id) : agents.find(a => a.id === l.id);

  return (
    <div className="tree mt-4">
      <ul>
        <li>
          <div className="node org">
            <div className="nm">{t("org.rootOrg")}</div>
            <div className="rl">{t("org.rootOrgSub", { count: humans.length + agents.length })}</div>
          </div>
          <ul>
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
  members, parentId, seen, dragging, onDragStart, onDragEnd, onDrop,
}: {
  members: Members;
  parentId?: string;      // undefined = roots der Abteilung
  seen: Set<string>;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
}) {
  const childHumans = parentId === undefined
    ? rootsOf(members).humans
    : members.humans.filter(h => h.manager_id === parentId && !seen.has(h.id));
  const childAgents = parentId === undefined
    ? rootsOf(members).agents
    : members.agents.filter(a => a.supervisor_id === parentId && !seen.has(a.id));

  if (childHumans.length + childAgents.length === 0) return null;

  return (
    <ul>
      {childHumans.map(h => (
        <MemberNode
          key={h.id}
          member={h}
          kind="human"
          members={members}
          seen={seen}
          dragging={dragging}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
        />
      ))}
      {childAgents.map(a => (
        <MemberNode
          key={a.id}
          member={a}
          kind="agent"
          members={members}
          seen={seen}
          dragging={dragging}
          onDragStart={onDragStart}
          onDragEnd={onDragEnd}
          onDrop={onDrop}
        />
      ))}
    </ul>
  );
}

function MemberNode({
  member, kind, members, seen, dragging, onDragStart, onDragEnd, onDrop,
}: {
  member: Human | Agent;
  kind: "human" | "agent";
  members: Members;
  seen: Set<string>;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const isAgent = kind === "agent";
  const agent = isAgent ? (member as Agent) : null;
  const human = !isAgent ? (member as Human) : null;

  const beingDragged = dragging?.member.id === member.id;
  // Menschen können nur Menschen unterstellt werden (manager_id → humans);
  // Agenten dürfen auf beides fallen.
  const canDrop = !!dragging && dragging.member.id !== member.id
    && (dragging.kind === "agent" || !isAgent);
  const status = agent ? (agent.killed ? "killed" : agent.status) : "";
  const nextSeen = new Set(seen).add(member.id);
  const deptId = (member.department_id ?? null) as string | null;

  return (
    <li>
      <div
        className={`orgmember${beingDragged ? " orgmember-out" : ""}${isOver && canDrop ? " node-drop-over" : ""}`}
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
              {isAgent ? (agent!.job_title || agent!.slug) : (human!.job_title || roleLabel[human!.role] || human!.role)}
            </div>
          </div>
          {isAgent
            ? <span className={`badge st-${status}`}>{t(`status.${status}`, status)}</span>
            : <span className="ntag">{t("org.nodeHuman")}</span>}
        </Link>
      </div>
      <MemberBranch
        members={members}
        parentId={member.id}
        seen={nextSeen}
        dragging={dragging}
        onDragStart={onDragStart}
        onDragEnd={onDragEnd}
        onDrop={onDrop}
      />
    </li>
  );
}

function DeptTreeNode({
  dept, members, resolveLead, dragging, onDragStart, onDragEnd, onDrop, onUpdate,
}: {
  dept: Department;
  members: Members;
  resolveLead: (l: DeptLead) => Human | Agent | undefined;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
  onUpdate: () => void;
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
              <span className="nm">{dept.name}</span>
              <button className="icon-btn" onClick={() => setRenaming(true)} title={t("org.renameDept")} style={{ fontSize: 12 }}>✎</button>
              <button className="icon-btn danger" onClick={() => setConfirmDelete(true)} title={t("org.deleteDept")} style={{ fontSize: 12 }}>✕</button>
            </div>
            <div className="rl">{total}&thinsp;{total === 1 ? t("org.member") : t("org.members")}</div>
          </>
        )}

        {/* Leitung: Chips der aktuellen Leitungen. */}
        {dept.leads.length > 0 && (
          <div className="dept-leads">
            <span className="dept-leads-label">{t("org.leadLabel")}</span>
            {dept.leads.map(l => {
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

      {total > 0 ? (
        <MemberBranch members={members} seen={new Set()} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} onDrop={onDrop} />
      ) : (
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
  members, dragging, onDragStart, onDragEnd, onDrop,
}: {
  members: Members;
  dragging: DragItem | null;
  onDragStart: (d: DragItem) => void;
  onDragEnd: () => void;
  onDrop: (t: DropTarget) => void;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const total = members.humans.length + members.agents.length;

  return (
    <li>
      <div
        className={`node dept unassigned${isOver && dragging ? " node-drop-over" : ""}`}
        onDragOver={e => { if (dragging) { e.preventDefault(); e.stopPropagation(); setIsOver(true); } }}
        onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false); }}
        onDrop={e => { e.preventDefault(); e.stopPropagation(); setIsOver(false); onDrop({ deptId: null, supervisorId: null }); }}
      >
        <div className="nm">{t("org.diagramUnassigned")}</div>
        <div className="rl">{total}&thinsp;{total === 1 ? t("org.member") : t("org.members")}</div>
      </div>
      {total > 0 && (
        <MemberBranch members={members} seen={new Set()} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} onDrop={onDrop} />
      )}
    </li>
  );
}
