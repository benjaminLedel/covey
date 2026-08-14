import { useState } from "react";
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
  snapshots: number;
  oldest?: string;
  latest?: Snapshot;
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
  const [busy, setBusy] = useState<string | null>(null);

  const home = useQuery({
    queryKey: ["agent-home", agent.id],
    queryFn: () => api<HomeView>(`/agents/${agent.id}/home`),
  });
  const snapshots = useQuery({
    queryKey: ["agent-snapshots", agent.id],
    queryFn: () => api<Snapshot[]>(`/agents/${agent.id}/home/snapshots`),
    enabled: !!home.data?.enabled,
  });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["agent-home", agent.id] });
    qc.invalidateQueries({ queryKey: ["agent-snapshots", agent.id] });
  };
  const backup = useMutation({
    mutationFn: () => post(`/agents/${agent.id}/home/snapshots`, {}),
    onSuccess: invalidate,
  });
  const restore = useMutation({
    mutationFn: (snapshot: string) => post(`/agents/${agent.id}/home/restore`, { snapshot }),
    onSuccess: invalidate,
    onSettled: () => setBusy(null),
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
            <div className="muted">
              {t("agent.home.snapshots", {
                count: home.data.snapshots,
                since: home.data.oldest ? fmtDate(home.data.oldest) : "—",
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

      {snapshots.data && snapshots.data.length > 0 && (
        <details>
          <summary className="text-xs">{t("agent.home.history")}</summary>
          <table className="tbl text-xs" style={{ marginTop: 6 }}>
            <tbody>
              {snapshots.data.map((snap) => (
                <tr key={snap.id}>
                  <td>{fmtDate(snap.created_at)}</td>
                  <td className="muted">{t(`agent.home.reason.${snap.reason}`, snap.reason)}</td>
                  <td className="text-right">{fmtBytes(snap.total_size)}</td>
                  <td className="text-right">
                    {/* Wiederherstellen ist ein verändernder Eingriff in
                        fremde Arbeit: nur mit Rolle, nur mit Bestätigung, und
                        nur solange der Agent nicht läuft — sonst schriebe die
                        laufende Sandbox in ein Home, das sich unter ihr ändert.
                        Die letzte Bedingung prüft der Server. */}
                    {canWrite && snap.id !== latest?.id && (
                      <button
                        className="btn-ghost text-xs"
                        disabled={busy === snap.id}
                        onClick={() => {
                          if (!confirm(t("agent.home.confirmRestore", { when: fmtDate(snap.created_at) }))) return;
                          setBusy(snap.id);
                          restore.mutate(snap.id);
                        }}
                      >
                        {t("agent.home.restore")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {restore.isError && (
            <p className="danger-text text-xs">{(restore.error as Error).message}</p>
          )}
        </details>
      )}
    </div>
  );
}
