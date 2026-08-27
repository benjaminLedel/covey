import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, type Agent } from "../api";
import { fmtBytes } from "../format";

// Das Home im Home-Store (spec/16, „Interface"). Die interessante Zahl ist
// nicht die Größe, sondern die Differenz: ein 7-GB-Home, von dem vielleicht
// 200 MB nur dieser Agent hält. Das erste kostet beim Verlust Zeit, das zweite
// Arbeit.

type HomeView = {
  enabled: boolean;
  latest?: Snapshot;
  // Ein Versuch, der KEINEN Schnappschuss ergeben hat und jünger ist als der
  // letzte, der einen ergab. Ohne ihn zeigte diese Ansicht den letzten
  // geglückten Stand — wahr und nutzlos, während seither jeder Versuch
  // scheiterte.
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

      {/* Zuerst, und in Warnfarbe: was hier steht, entwertet alle Zahlen
          darunter. Der Schnappschuss stimmt, aber er ist nicht der Stand des
          Arbeitsplatzes. */}
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
            {/* Die Differenz ist die eigentliche Aussage — und sie braucht
                den Satz daneben, sonst sind es zwei Zahlen ohne Bedeutung. */}
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

          {/* Beantwortet „warum ist dieses Home so groß?" ohne Shell — und
              zeigt zugleich die Kandidaten für einen Ausschluss. */}
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
