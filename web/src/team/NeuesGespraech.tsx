import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router";
import { api, createGroup, openDirect, reachableAgents, type MemberRef, type OrgChart, type Principal } from "../api";
import { Modal } from "../components/Modal";
import { Avatar } from "../components/person";
import { canManage } from "../pages/agent/roles";
import { NavIcon } from "../components/navicons";

/* Starting a conversation (#440): one person or colleague picked is a direct
 * conversation — the one that exists, or a new one; several are a group,
 * which needs a title. The agents offered are the ones the organisation's
 * reach lets the person write to; a group with agents needs the right to
 * create a task by hand besides (the server says the same). A direct
 * conversation with an agent opens in the agent's thread. */
export default function NeuesGespraech({ me, onClose }: { me: Principal; onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [suche, setSuche] = useState("");
  const [titel, setTitel] = useState("");
  const [gewaehlt, setGewaehlt] = useState<MemberRef[]>([]);
  const chart = useQuery({ queryKey: ["org-chart"], queryFn: () => api<OrgChart>("/org/chart") });
  const erreichbar = useQuery({ queryKey: ["reachable-agents"], queryFn: reachableAgents, staleTime: 60_000 });
  const erreicht = new Set(erreichbar.data?.agents ?? []);

  const q = suche.trim().toLowerCase();
  const passt = (name: string, extra = "") => !q || (name + " " + extra).toLowerCase().includes(q);
  const menschen = (chart.data?.humans ?? []).filter((h) => h.id !== me.ID && passt(h.display_name, h.email));
  const agenten = (chart.data?.agents ?? []).filter(
    (a) => a.hired_at && !a.killed && erreicht.has(a.id) && passt(a.display_name, a.slug),
  );
  const ist = (r: MemberRef) => gewaehlt.some((g) => g.kind === r.kind && g.id === r.id);
  const umschalten = (r: MemberRef) =>
    setGewaehlt((alt) => (ist(r) ? alt.filter((g) => !(g.kind === r.kind && g.id === r.id)) : [...alt, r]));
  const gruppe = gewaehlt.length > 1;
  const sucheRef = useRef<HTMLInputElement>(null);
  const nameVon = (g: MemberRef): { name: string; slug?: string } => {
    if (g.kind === "human") return { name: chart.data?.humans.find((h) => h.id === g.id)?.display_name ?? "" };
    const a = chart.data?.agents.find((x) => x.id === g.id);
    return { name: a?.display_name ?? "", slug: a?.slug };
  };
  // A group with an agent is the manage roles' (conversations.go).
  const gruppeGesperrt = gruppe && gewaehlt.some((g) => g.kind === "agent") && !canManage(me.Role);

  const los = useMutation({
    mutationFn: async () => (gruppe ? createGroup(titel.trim(), gewaehlt) : openDirect(gewaehlt[0])),
    onSuccess: (c) => {
      qc.invalidateQueries({ queryKey: ["conversations"] });
      onClose();
      const agent = c.kind === "direct" ? c.members.find((m) => m.kind === "agent") : undefined;
      navigate(agent ? `/team/${agent.id}` : `/team/c/${c.id}`);
    },
  });

  const zeile = (r: MemberRef, name: string, unter: string, slug?: string) => (
    <li key={`${r.kind}:${r.id}`}>
      <label className="tm-wahl">
        <input type="checkbox" checked={ist(r)} onChange={() => umschalten(r)} />
        <Avatar name={name} human={r.kind === "human"} slug={slug} size={22} />
        <span className="tm-wahl-name">{name}</span>
        <span className="tm-leise">{unter}</span>
      </label>
    </li>
  );

  return (
    <Modal
      title={t("conversation.newTitle")}
      onClose={onClose}
      footer={
        <div className="flex gap-2 justify-end">
          <button className="btn" onClick={onClose}>
            {t("team.abbrechen")}
          </button>
          <button
            className="btn primary"
            disabled={gewaehlt.length === 0 || (gruppe && !titel.trim()) || gruppeGesperrt || los.isPending}
            onClick={() => los.mutate()}
          >
            {gruppe ? t("conversation.startGroup") : t("conversation.startDirect")}
          </button>
        </div>
      }
    >
      <p className="muted text-xs" style={{ marginBottom: 10 }}>
        {t("conversation.newLead")}
      </p>
      {/* The recipients as chips in the field one searches in, the way a
          mail's To line works: what is picked stays in sight while one keeps
          typing, and Backspace in an empty field takes the last one back. */}
      <div className="tm-an" onClick={() => sucheRef.current?.focus()}>
        <NavIcon name="search" />
        {gewaehlt.map((g) => {
          const n = nameVon(g);
          return (
            <span key={`${g.kind}:${g.id}`} className="tm-an-chip">
              <Avatar name={n.name} human={g.kind === "human"} slug={n.slug} size={18} />
              <span className="truncate">{n.name}</span>
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  umschalten(g);
                }}
                aria-label={t("conversation.removePicked", { name: n.name })}
                title={t("conversation.removePicked", { name: n.name })}
              >
                <svg viewBox="0 0 24 24" className="ic" aria-hidden="true">
                  <path d="M7 7l10 10M17 7L7 17" />
                </svg>
              </button>
            </span>
          );
        })}
        <input
          ref={sucheRef}
          value={suche}
          onChange={(e) => setSuche(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Backspace" && suche === "" && gewaehlt.length > 0) umschalten(gewaehlt[gewaehlt.length - 1]);
          }}
          placeholder={gewaehlt.length ? t("conversation.searchMore") : t("conversation.search")}
          aria-label={t("conversation.search")}
          autoFocus
        />
      </div>
      {/* The name only once there is a group to name: with one pick the
          dialog opens a direct conversation, and a field for it would only ask
          a question that has no answer. */}
      {gruppe && (
        <div className="tm-titel-feld">
          <label htmlFor="tm-gruppe-titel">{t("conversation.groupTitle")}</label>
          <input
            id="tm-gruppe-titel"
            value={titel}
            onChange={(e) => setTitel(e.target.value)}
            placeholder={t("conversation.groupTitlePlaceholder")}
            maxLength={120}
            aria-describedby="tm-gruppe-titel-hinweis"
          />
          <span id="tm-gruppe-titel-hinweis" className="muted text-xs">
            {titel.trim() ? t("conversation.groupSize", { count: gewaehlt.length + 1 }) : t("conversation.groupTitleNeeded")}
          </span>
        </div>
      )}
      {chart.isLoading && <p className="muted text-xs">{t("common.loading")}</p>}
      {menschen.length > 0 && (
        <>
          <h3 className="tm-gruppe-kopf">{t("conversation.people")}</h3>
          <ul className="tm-wahl-liste">
            {menschen.map((h) => zeile({ kind: "human", id: h.id }, h.display_name, h.job_title || h.email))}
          </ul>
        </>
      )}
      {agenten.length > 0 && (
        <>
          <h3 className="tm-gruppe-kopf">{t("conversation.agents")}</h3>
          <ul className="tm-wahl-liste">
            {agenten.map((a) => zeile({ kind: "agent", id: a.id }, a.display_name, a.job_title || a.slug, a.slug))}
          </ul>
        </>
      )}
      {gruppeGesperrt && <p className="muted text-xs">{t("conversation.groupAgentsManage")}</p>}
      {los.isError && <p className="danger-text text-xs">{String((los.error as Error)?.message ?? los.error)}</p>}
    </Modal>
  );
}
