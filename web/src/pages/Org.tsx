import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  api, roleLabel,
  createDepartment, renameDepartment, deleteDepartment, setAgentDepartment,
  type Agent, type Human, type OrgChart, type Department,
} from "../api";
import { Avatar } from "../components/person";

export default function Org() {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [dragging, setDragging] = useState<Agent | null>(null);
  const [showNewDept, setShowNewDept] = useState(false);
  const [newName, setNewName] = useState("");
  const [newDesc, setNewDesc] = useState("");

  const chart = useQuery({
    queryKey: ["orgchart"],
    queryFn: () => api<OrgChart>("/org/chart"),
  });

  const createMut = useMutation({
    mutationFn: ({ name, desc }: { name: string; desc: string }) =>
      createDepartment(name, desc),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["orgchart"] });
      setNewName("");
      setNewDesc("");
      setShowNewDept(false);
    },
  });

  const deptMut = useMutation({
    mutationFn: ({ agentId, deptId }: { agentId: string; deptId: string | null }) =>
      setAgentDepartment(agentId, deptId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["orgchart"] }),
  });

  const onDrop = (deptId: string | null) => {
    if (dragging) deptMut.mutate({ agentId: dragging.id, deptId });
    setDragging(null);
  };

  if (chart.isLoading) return null;
  if (chart.isError) return <p className="danger-text">{t("org.loadError")}</p>;
  const { humans, agents, departments } = chart.data!;

  const unassignedAgents = agents.filter(a => !a.department_id);
  const unassignedHumans = humans.filter(h => !h.department_id);

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
            createMut.mutate({ name: newName, desc: newDesc });
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
          <button className="btn sm primary" type="submit" disabled={createMut.isPending}>
            {t("org.createDept")}
          </button>
          <button type="button" className="btn sm" onClick={() => setShowNewDept(false)}>
            {t("modal.cancel")}
          </button>
        </form>
      )}

      {/* Abteilungs-Karten */}
      {departments.length === 0 && !showNewDept ? (
        <p className="muted mt-4">{t("org.noDepts")}</p>
      ) : (
        <div className="dept-entity-grid">
          {departments.map(dept => (
            <DeptEntityCard
              key={dept.id}
              dept={dept}
              humans={humans.filter(h => h.department_id === dept.id)}
              agents={agents.filter(a => a.department_id === dept.id)}
              dragging={dragging}
              onDragStart={setDragging}
              onDragEnd={() => setDragging(null)}
              onDrop={() => onDrop(dept.id)}
              onUpdate={invalidate}
            />
          ))}
        </div>
      )}

      {/* Ohne Abteilung */}
      {(unassignedAgents.length > 0 || unassignedHumans.length > 0) && (
        <UnassignedSection
          agents={unassignedAgents}
          humans={unassignedHumans}
          dragging={dragging}
          onDragStart={setDragging}
          onDragEnd={() => setDragging(null)}
          onDrop={() => onDrop(null)}
        />
      )}
    </div>
  );
}

function DeptEntityCard({
  dept, humans, agents, dragging, onDragStart, onDragEnd, onDrop, onUpdate,
}: {
  dept: Department;
  humans: Human[];
  agents: Agent[];
  dragging: Agent | null;
  onDragStart: (a: Agent) => void;
  onDragEnd: () => void;
  onDrop: () => void;
  onUpdate: () => void;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
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

  const handleDragOver = (e: React.DragEvent) => {
    if (!dragging) return;
    e.preventDefault();
    e.stopPropagation();
    setIsOver(true);
  };
  const handleDragLeave = (e: React.DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false);
  };
  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsOver(false);
    onDrop();
  };

  const total = humans.length + agents.length;

  return (
    <div
      className={`dept-entity-card${isOver && dragging ? " dept-drop-over" : ""}`}
      onDragOver={handleDragOver}
      onDragLeave={handleDragLeave}
      onDrop={handleDrop}
    >
      <div className="dept-hdr">
        {renaming ? (
          <form
            style={{ display: "flex", gap: 4, flex: 1, alignItems: "center" }}
            onSubmit={e => { e.preventDefault(); renameMut.mutate(); }}
          >
            <input
              value={editName}
              onChange={e => setEditName(e.target.value)}
              autoFocus
              required
              style={{ flex: 1, height: 26, fontSize: 12 }}
            />
            <button className="btn sm primary" type="submit" disabled={renameMut.isPending} style={{ padding: "3px 8px" }}>✓</button>
            <button type="button" className="btn sm" style={{ padding: "3px 8px" }} onClick={() => { setRenaming(false); setEditName(dept.name); }}>✕</button>
          </form>
        ) : (
          <>
            <span className="dept-lbl">{dept.name}</span>
            {dept.description && (
              <span className="muted" style={{ fontSize: 11, marginLeft: 4, whiteSpace: "nowrap", overflow: "hidden", textOverflow: "ellipsis", maxWidth: 180 }}>
                {dept.description}
              </span>
            )}
            <span className="dept-count">{total}&thinsp;{total === 1 ? t("org.member") : t("org.members")}</span>
            <button className="icon-btn" onClick={() => setRenaming(true)} title={t("org.renameDept")} style={{ fontSize: 12 }}>
              ✎
            </button>
            <button className="icon-btn danger" onClick={() => setConfirmDelete(true)} title={t("org.deleteDept")} style={{ fontSize: 12 }}>
              ✕
            </button>
            <button
              className={`dept-toggle-btn${collapsed ? " closed" : ""}`}
              onClick={() => setCollapsed(v => !v)}
              title={collapsed ? t("org.expand") : t("org.collapse")}
            >
              <svg viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <path d={collapsed ? "M6 4l4 4-4 4" : "M4 6l4 4 4-4"} />
              </svg>
            </button>
          </>
        )}
      </div>

      {confirmDelete && (
        <div className="dept-delete-confirm">
          <span style={{ flex: 1 }}>{t("org.deleteDeptConfirm", { name: dept.name })}</span>
          <button className="btn sm danger" onClick={() => deleteMut.mutate()} disabled={deleteMut.isPending}>
            {t("org.deleteDept")}
          </button>
          <button className="btn sm" onClick={() => setConfirmDelete(false)}>{t("modal.cancel")}</button>
        </div>
      )}

      {!collapsed && (
        <div className="dept-entity-body">
          {humans.map(h => (
            <Link key={h.id} to={`/people/${h.id}`} className="node human dept-member-node">
              <Avatar name={h.display_name} human />
              <div>
                <div className="nm">{h.display_name}</div>
                <div className="rl">{h.job_title || roleLabel[h.role] || h.role}</div>
              </div>
            </Link>
          ))}
          {agents.map(a => (
            <AgentChip key={a.id} agent={a} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} />
          ))}
          {total === 0 && (
            <span className="dept-drop-hint">{dragging ? t("org.dropHere") : t("org.dragHint")}</span>
          )}
        </div>
      )}
    </div>
  );
}

function UnassignedSection({
  agents, humans, dragging, onDragStart, onDragEnd, onDrop,
}: {
  agents: Agent[];
  humans: Human[];
  dragging: Agent | null;
  onDragStart: (a: Agent) => void;
  onDragEnd: () => void;
  onDrop: () => void;
}) {
  const { t } = useTranslation();
  const [isOver, setIsOver] = useState(false);
  const count = agents.length + humans.length;

  return (
    <div className="mt-6">
      <h2 className="text-sm mb-1" style={{ color: "var(--text-secondary)", fontWeight: 500, marginTop: 0 }}>
        {t("org.unassigned", { count })}
      </h2>
      <p className="muted text-xs mb-3">{t("org.unassignedHint")}</p>
      <div
        className={`dept-unassigned${isOver && dragging ? " dept-drop-over" : ""}`}
        onDragOver={e => { if (dragging) { e.preventDefault(); setIsOver(true); } }}
        onDragLeave={e => { if (!e.currentTarget.contains(e.relatedTarget as Node)) setIsOver(false); }}
        onDrop={e => { e.preventDefault(); setIsOver(false); onDrop(); }}
      >
        {humans.map(h => (
          <Link key={h.id} to={`/people/${h.id}`} className="node human dept-member-node">
            <Avatar name={h.display_name} human />
            <div>
              <div className="nm">{h.display_name}</div>
              <div className="rl">{h.job_title || roleLabel[h.role] || h.role}</div>
            </div>
          </Link>
        ))}
        {agents.map(a => (
          <AgentChip key={a.id} agent={a} dragging={dragging} onDragStart={onDragStart} onDragEnd={onDragEnd} />
        ))}
      </div>
    </div>
  );
}

function AgentChip({
  agent, dragging, onDragStart, onDragEnd,
}: {
  agent: Agent;
  dragging: Agent | null;
  onDragStart: (a: Agent) => void;
  onDragEnd: () => void;
}) {
  const { t } = useTranslation();
  const status = agent.killed ? "killed" : agent.status;
  const isMe = dragging?.id === agent.id;

  return (
    <div
      className={`agent-chip${isMe ? " agent-chip-out" : ""}`}
      draggable
      onDragStart={e => { e.dataTransfer.effectAllowed = "move"; onDragStart(agent); }}
      onDragEnd={onDragEnd}
    >
      <span className="agent-grip" title={t("org.dragAgent")}>
        <svg viewBox="0 0 10 16" fill="currentColor">
          <circle cx="3" cy="3" r="1.2" />
          <circle cx="7" cy="3" r="1.2" />
          <circle cx="3" cy="8" r="1.2" />
          <circle cx="7" cy="8" r="1.2" />
          <circle cx="3" cy="13" r="1.2" />
          <circle cx="7" cy="13" r="1.2" />
        </svg>
      </span>
      <Link to={`/agents/${agent.id}`} className="node agent" draggable={false}>
        <Avatar name={agent.display_name} />
        <div>
          <div className="nm">{agent.display_name}</div>
          <div className="rl mono">{agent.slug}</div>
        </div>
        <span className={`badge st-${status}`}>{t(`status.${status}`, status)}</span>
      </Link>
    </div>
  );
}
