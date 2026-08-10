import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, hireAgent, type Agent, type AgentSystem, type OrgChart } from "../api";
import { Modal } from "./Modal";

/* Einstellen — der eine Weg aus dem Entwurf.
 *
 * Bewusst keine Rückfrage („Wirklich einstellen?"), sondern eine
 * Zusammenfassung: Rolle, angefragte Zielsysteme mit Scopes, Vorgesetzter,
 * Runtime, Budgetdeckel. Das ist der einzige Punkt im ganzen Ablauf, an dem
 * ein Mensch die Verantwortung für einen neuen Mitarbeiter übernimmt — also
 * sieht er auch aus wie eine Entscheidung und nicht wie ein Schalter.
 *
 * Was hier steht, ist genau das, was danach wirksam wird: ein Zugang ohne
 * Freigabe steht als „beantragt" da und nicht als erledigt. */
export function HireDialog({
  agent,
  onClose,
  onHired,
}: {
  agent: Agent;
  onClose: () => void;
  onHired?: (a: Agent) => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();

  const systems = useQuery({
    queryKey: ["agent-systems", agent.id],
    queryFn: () => api<AgentSystem[]>(`/agents/${agent.id}/systems`),
  });
  const org = useQuery({ queryKey: ["org-chart"], queryFn: () => api<OrgChart>("/org/chart") });

  const mut = useMutation({
    mutationFn: () => hireAgent(agent.id),
    onSuccess: (a) => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      qc.invalidateQueries({ queryKey: ["agent", agent.id] });
      qc.invalidateQueries({ queryKey: ["onboarding"] });
      onHired?.(a);
      onClose();
    },
  });

  const wanted = (systems.data ?? []).filter((s) => s.access);
  const boss = (org.data?.humans ?? []).find((h) => h.id === agent.supervisor_id);
  const dept = (org.data?.departments ?? []).find((d) => d.id === agent.department_id);

  return (
    <Modal
      title={t("hire.title", { name: agent.display_name })}
      onClose={onClose}
      footer={
        <div className="flex gap-2 justify-end">
          <button className="btn" onClick={onClose}>
            {t("dashboard.back")}
          </button>
          <button className="btn primary" onClick={() => mut.mutate()} disabled={mut.isPending}>
            {mut.isPending ? t("hire.hiring") : t("hire.confirm")}
          </button>
        </div>
      }
    >
      <p className="muted text-xs" style={{ marginBottom: 14, maxWidth: 560 }}>
        {t("hire.lead")}
      </p>

      <dl className="hire-summary">
        <Row label={t("hire.role")} value={agent.job_title || t("hire.noRole")} />
        <Row label={t("dashboard.selectRuntime")} value={agent.runtime} mono />
        <Row label={t("hire.supervisor")} value={boss?.display_name ?? "—"} />
        <Row label={t("hire.department")} value={dept?.name ?? "—"} />
        <Row
          label={t("hire.budget")}
          value={agent.budget_usd > 0 ? `${agent.budget_usd.toFixed(2)} $` : t("hire.noBudget")}
        />
      </dl>

      <div style={{ marginTop: 14 }}>
        <div className="text-xs" style={{ fontWeight: 600, marginBottom: 6 }}>
          {t("hire.systems")}
        </div>
        {systems.isLoading && <p className="muted text-xs">…</p>}
        {!systems.isLoading && wanted.length === 0 && (
          <p className="muted text-xs">{t("hire.noSystems")}</p>
        )}
        <ul className="hire-systems">
          {wanted.map((s) => (
            <li key={s.name}>
              <span>{s.label || s.name}</span>
              {s.scopes && s.scopes.length > 0 && (
                <span className="mono muted text-xs"> {s.scopes.join(", ")}</span>
              )}
              {!s.enabled && (
                <span className="badge st-blocked" style={{ marginLeft: 8 }}>
                  {t("hire.systemPending")}
                </span>
              )}
            </li>
          ))}
        </ul>
      </div>

      {mut.isError && (
        <div className="danger-text text-xs" style={{ marginTop: 10 }}>
          {String((mut.error as Error)?.message ?? mut.error)}
        </div>
      )}
    </Modal>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <>
      <dt className="muted text-xs">{label}</dt>
      <dd className={`text-sm ${mono ? "mono" : ""}`}>{value}</dd>
    </>
  );
}
