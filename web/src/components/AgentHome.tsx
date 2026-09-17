import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, type Agent } from "../api";
import { fmtBytes } from "../format";

// The home in the home store (spec/16, "Interface"). The interesting number is
// not the size but the difference: a 7 GB home of which this agent alone may
// hold 200 MB. Losing the first costs time, losing the second
// costs work.

type HomeView = {
  enabled: boolean;
  latest?: Snapshot;
  // An attempt that yielded NO snapshot and is newer than the last one that
  // yielded one. Without it this view showed the last good state — true and
  // useless, because every attempt since then
  // failed.
  last_failure?: { at: string; error: string; reason?: string };
  runner_name?: string;
  runner_kind?: string;
  total_bytes: number;
  exclusive_bytes: number;
  top_dirs?: { path: string; bytes: number; files: number }[];
};

type Snapshot = {
  id: string;
  manifest_hash: string;
  total_size: number;
  blocks_up: number;
  bytes_up: number;
  duration_ms: number;
  reason: string;
  created_at: string;
};

export function AgentHome({ agent, canWrite }: { agent: Agent; canWrite: boolean }) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();

  const home = useQuery({
    queryKey: ["agent-home", agent.id],
    queryFn: () => api<HomeView>(`/agents/${agent.id}/home`),
  });
  const backup = useMutation({
    mutationFn: () => post(`/agents/${agent.id}/home/snapshots`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["agent-home", agent.id] }),
  });

  if (!home.data) return null;
  if (!home.data.enabled) {
    return (
      <p className="muted text-xs card p-4" style={{ marginTop: 16 }}>
        {t("agent.home.disabled")}
      </p>
    );
  }

  const fmtDate = (iso: string) => new Date(iso).toLocaleString(i18n.language);
  const latest = home.data.latest;

  return (
    <div className="card p-4 flex flex-col gap-2" style={{ marginTop: 16 }}>
      <h3 className="text-[15px]">{t("agent.home.title")}</h3>

      {/* First, and in warning colour: what stands here devalues every number
          below it. The snapshot is right, but it is not the state of the
          workplace. */}
      {home.data.last_failure && (
        <p className="text-xs danger-text">
          {t("agent.home.syncFailed", {
            when: fmtDate(home.data.last_failure.at),
            error: home.data.last_failure.error,
          })}
        </p>
      )}

      {!latest && <p className="muted text-xs">{t("agent.home.noSnapshot")}</p>}

      {latest && (
        <>
          <div className="text-xs flex flex-col gap-1">
            {/* The difference is the real statement — it needs the sentence
                beside it, else the two numbers mean nothing. */}
            <div>
              {t("agent.home.size", {
                total: fmtBytes(home.data.total_bytes),
                exclusive: fmtBytes(home.data.exclusive_bytes),
              })}
            </div>
            <div className="muted">{t("agent.home.sizeHint")}</div>
            <div className="muted">
              {t("agent.home.lastSync", {
                when: fmtDate(latest.created_at),
                blocks: latest.blocks_up,
                bytes: fmtBytes(latest.bytes_up),
                seconds: (latest.duration_ms / 1000).toFixed(1),
              })}
            </div>
            {home.data.runner_kind && (
              <div className="muted">
                {t("agent.home.runner", {
                  runner:
                    home.data.runner_kind === "builtin"
                      ? t("runners.builtin")
                      : home.data.runner_name || "—",
                })}
              </div>
            )}
          </div>

          {/* Answers "why is this home so large?" without a shell — and
              shows the candidates for an exclusion at the same time. */}
          {home.data.top_dirs?.length ? (
            <table className="tbl text-xs">
              <tbody>
                {home.data.top_dirs.map((d) => (
                  <tr key={d.path}>
                    <td className="mono">{d.path}</td>
                    <td className="text-right">{fmtBytes(d.bytes)}</td>
                    <td className="text-right muted">{t("agent.home.files", { count: d.files })}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : null}
        </>
      )}

      {canWrite && (
        <div>
          <button className="btn-ghost text-xs" onClick={() => backup.mutate()} disabled={backup.isPending}>
            {t("agent.home.backupNow")}
          </button>
          {backup.isError && (
            <span className="danger-text text-xs" style={{ marginLeft: 8 }}>
              {(backup.error as Error).message}
            </span>
          )}
        </div>
      )}

    </div>
  );
}
