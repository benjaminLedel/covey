import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, post, statusLabel, type Approval } from "../api";

export default function Approvals() {
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

  const list = approvals.data ?? [];
  const pending = list.filter((a) => a.status === "pending");
  const decided = list.filter((a) => a.status !== "pending");

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-4">
        <h1 className="text-[22px]">Freigaben</h1>
        <span className="muted">{pending.length} ausstehend</span>
      </div>

      {pending.length === 0 && <p className="muted">Nichts wartet auf Freigabe.</p>}
      {pending.map((a) => (
        <div key={a.id} className="card mb-2 flex items-center gap-4">
          <div className="flex-1 min-w-0">
            <div className="font-medium text-sm mono">{a.action}</div>
            <div className="muted text-xs break-all">
              {JSON.stringify(a.params)} · angefragt {new Date(a.requested_at).toLocaleString("de-DE")}
            </div>
          </div>
          <button
            className="btn sm primary"
            disabled={decide.isPending}
            onClick={() => decide.mutate({ id: a.id, approve: true })}
          >
            Freigeben
          </button>
          <button
            className="btn sm danger"
            disabled={decide.isPending}
            onClick={() => decide.mutate({ id: a.id, approve: false })}
          >
            Ablehnen
          </button>
        </div>
      ))}

      {decided.length > 0 && (
        <>
          <h2 className="text-base mt-6 mb-2 secondary">Entschieden</h2>
          {decided.map((a) => (
            <div key={a.id} className="card mb-2 flex items-center gap-4" style={{ padding: "10px 15px" }}>
              <span className={`badge st-${a.status}`}>{statusLabel[a.status] ?? a.status}</span>
              <span className="mono text-sm flex-1 min-w-0 truncate">{a.action}</span>
              <span className="muted text-xs">{new Date(a.requested_at).toLocaleString("de-DE")}</span>
            </div>
          ))}
        </>
      )}
    </div>
  );
}
