import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, type Approval } from "../api";

export default function Approvals() {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const approvals = useQuery({
    queryKey: ["approvals", "all"],
    queryFn: () => api<Approval[] | null>("/approvals"),
    refetchInterval: 10000,
  });
  const decide = useMutation({
    mutationFn: ({ id, approve }: { id: string; approve: boolean }) =>
      post(`/approvals/${id}/decide`, { approve }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["approvals"] }),
  });

  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const list = approvals.data ?? [];
  const pending = list.filter((a) => a.status === "pending");
  const decided = list.filter((a) => a.status !== "pending");

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-4">
        <h1 className="text-[22px]">{t("approvals.title")}</h1>
        <span className="muted">{t("approvals.pending", { count: pending.length })}</span>
      </div>

      {pending.length === 0 && <p className="muted">{t("approvals.noPending")}</p>}
      {pending.map((a) => (
        <div key={a.id} className="card mb-2 flex items-center gap-4">
          <div className="flex-1 min-w-0">
            <div className="font-medium text-sm mono">{a.action}</div>
            <div className="muted text-xs break-all">
              {JSON.stringify(a.params)} · {t("approvals.requested", { date: new Date(a.requested_at).toLocaleString(locale) })}
            </div>
          </div>
          <button
            className="btn sm primary"
            disabled={decide.isPending}
            onClick={() => decide.mutate({ id: a.id, approve: true })}
          >
            {t("approvals.approve")}
          </button>
          <button
            className="btn sm danger"
            disabled={decide.isPending}
            onClick={() => decide.mutate({ id: a.id, approve: false })}
          >
            {t("approvals.deny")}
          </button>
        </div>
      ))}

      {decided.length > 0 && (
        <>
          <h2 className="text-base mt-6 mb-2 secondary">{t("approvals.decided")}</h2>
          {decided.map((a) => (
            <div key={a.id} className="card mb-2 flex items-center gap-4" style={{ padding: "10px 15px" }}>
              <span className={`badge st-${a.status}`}>{t(`status.${a.status}`, a.status)}</span>
              <span className="mono text-sm flex-1 min-w-0 truncate">{a.action}</span>
              <span className="muted text-xs">{new Date(a.requested_at).toLocaleString(locale)}</span>
            </div>
          ))}
        </>
      )}
    </div>
  );
}
