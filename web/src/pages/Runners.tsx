import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post, patch, del, type Principal } from "../api";

// Die Runner-Ansicht (spec/16, Stufe 5). Ab dem dritten Runner ist sie das,
// was den Betrieb bedienbar macht: welche Hosts es gibt, welcher gerade traegt,
// und wo der Platz knapp wird — bevor er alle ist, nicht danach.

type RunnerView = {
  id: string;
  kind: "builtin" | "remote";
  name: string;
  description?: string;
  tags?: string[];
  version?: string;
  arch?: string;
  protocol?: number;
  last_seen_at?: string;
  created_at: string;
  live?: {
    connected: boolean;
    protocol: number;
    version?: string;
    arch?: string;
    tags?: string[];
    images?: string[];
    sandboxes: number;
    outdated: boolean;
  };
  capacity?: {
    sandboxes: number;
    total_bytes: number;
    free_bytes: number;
    work_dir?: string;
  };
};

type StoreView = {
  enabled: boolean;
  bytes: number;
  snapshots: number;
  agents: number;
  keep_per_agent: number;
  max_age_days: number;
};

type CleanupView = {
  snapshots: number;
  blocks_removed: number;
  freed_bytes: number;
  preview: boolean;
};

export function formatBytes(n: number): string {
  if (!n) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export default function Runners({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const manage = me.Role === "platform_admin" || me.Role === "agent_owner";
  const [token, setToken] = useState<string | null>(null);
  const [cleanup, setCleanup] = useState<CleanupView | null>(null);

  const runners = useQuery({
    queryKey: ["runners"],
    queryFn: () => api<RunnerView[]>("/runners"),
    // Der Live-Teil (verbunden, laufende Sandboxen, freier Platz) ist nur so
    // lange wahr, wie die Verbindung steht — deshalb nachladen statt einmal
    // holen.
    refetchInterval: 10_000,
  });
  const store = useQuery({
    queryKey: ["home-store"],
    queryFn: () => api<StoreView>("/platform/home-store"),
  });

  const createToken = useMutation({
    mutationFn: () => post<{ token: string }>("/runners/registration-tokens", {}),
    onSuccess: (r) => setToken(r.token),
  });
  const removeRunner = useMutation({
    mutationFn: (id: string) => del(`/runners/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["runners"] }),
  });
  const setRetention = useMutation({
    mutationFn: (v: { keep_per_agent: number; max_age_days: number }) =>
      patch("/platform/home-store", v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["home-store"] }),
  });
  const runCleanup = useMutation({
    mutationFn: (preview: boolean) =>
      post<CleanupView>(`/platform/home-store/cleanup?preview=${preview}`, {}),
    onSuccess: (r) => {
      setCleanup(r);
      if (!r.preview) qc.invalidateQueries({ queryKey: ["home-store"] });
    },
  });

  const list = runners.data ?? [];

  return (
    <div className="stack-lg">
      <div>
        <h1>{t("runners.title")}</h1>
        <p className="muted text-sm">{t("runners.intro")}</p>
      </div>

      <div className="card">
        {runners.isLoading && <p className="muted text-sm p-4">{t("common.loading")}</p>}
        {list.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th>{t("runners.colHost")}</th>
                <th>{t("runners.colState")}</th>
                <th>{t("runners.colTags")}</th>
                <th>{t("runners.colLoad")}</th>
                <th>{t("runners.colDisk")}</th>
                <th>{t("runners.colVersion")}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {list.map((r) => (
                <tr key={r.id}>
                  <td>
                    <div>{r.kind === "builtin" ? t("runners.builtin") : r.name || r.description || r.id.slice(0, 8)}</div>
                    {r.kind === "builtin" && (
                      <div className="muted text-xs">{t("runners.builtinHint")}</div>
                    )}
                    {r.capacity?.work_dir && <div className="muted text-xs mono">{r.capacity.work_dir}</div>}
                  </td>
                  <td>
                    {r.live?.connected ? (
                      <span className="badge ok">{t("runners.connected")}</span>
                    ) : (
                      <span className="badge" title={t("runners.offlineHint")}>
                        {t("runners.offline")}
                      </span>
                    )}
                    {/* Versionsversatz wird benannt, nicht bloss geduldet:
                        Runner und Server werden getrennt ausgeliefert. */}
                    {r.live?.outdated && (
                      <span className="badge warn" style={{ marginLeft: 6 }}>
                        {t("runners.outdated")}
                      </span>
                    )}
                  </td>
                  <td className="text-xs">
                    {(r.live?.tags ?? r.tags ?? []).join(", ") || <span className="muted">—</span>}
                    {r.live?.images?.length ? (
                      <div className="muted mono">{r.live.images.join(", ")}</div>
                    ) : null}
                  </td>
                  <td>{r.live ? t("runners.sandboxes", { count: r.capacity?.sandboxes ?? r.live.sandboxes }) : "—"}</td>
                  <td>
                    {r.capacity && r.capacity.total_bytes > 0 ? (
                      <DiskBar free={r.capacity.free_bytes} total={r.capacity.total_bytes} />
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="text-xs mono">{r.live?.version || r.version || "—"}</td>
                  <td className="text-right">
                    {/* Der eingebaute wird nicht verwaltet: er hat kein Token
                        zum Widerrufen und keinen Loeschknopf. Es gibt ihn, oder
                        die Regel sagt, dass es ihn nicht gibt. */}
                    {manage && r.kind === "remote" && (
                      <button
                        className="btn-ghost danger-text text-xs"
                        onClick={() => {
                          if (confirm(t("runners.confirmDelete"))) removeRunner.mutate(r.id);
                        }}
                      >
                        {t("runners.decommission")}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {manage && (
        <div className="card p-4 stack">
          <h2 className="text-sm">{t("runners.addTitle")}</h2>
          <p className="muted text-xs">{t("runners.addHint")}</p>
          <div>
            <button className="btn" onClick={() => createToken.mutate()} disabled={createToken.isPending}>
              {t("runners.newToken")}
            </button>
          </div>
          {token && (
            <div className="stack-sm">
              {/* Einmal im Klartext, danach nur noch als Hash. */}
              <p className="text-xs">{t("runners.tokenOnce")}</p>
              <pre className="mono text-xs" style={{ whiteSpace: "pre-wrap", wordBreak: "break-all" }}>
                {`covey-runner register --url ${window.location.origin} \\
  --token ${token} \\
  --description "…" --tag arm64`}
              </pre>
            </div>
          )}
        </div>
      )}

      {store.data?.enabled && (
        <div className="card p-4 stack">
          <h2 className="text-sm">{t("runners.storeTitle")}</h2>
          <p className="muted text-xs">{t("runners.storeHint")}</p>
          <div className="text-sm">
            {t("runners.storeSize", {
              size: formatBytes(store.data.bytes),
              snapshots: store.data.snapshots,
              agents: store.data.agents,
            })}
          </div>

          <div className="flex items-center gap-3 flex-wrap">
            <label className="text-xs">
              {t("runners.keepPerAgent")}{" "}
              <input
                type="number"
                min={0}
                defaultValue={store.data.keep_per_agent}
                className="mono"
                style={{ width: 70 }}
                onBlur={(e) =>
                  setRetention.mutate({
                    keep_per_agent: Number(e.target.value),
                    max_age_days: store.data!.max_age_days,
                  })
                }
              />
            </label>
            <label className="text-xs">
              {t("runners.maxAgeDays")}{" "}
              <input
                type="number"
                min={0}
                defaultValue={store.data.max_age_days}
                className="mono"
                style={{ width: 70 }}
                onBlur={(e) =>
                  setRetention.mutate({
                    keep_per_agent: store.data!.keep_per_agent,
                    max_age_days: Number(e.target.value),
                  })
                }
              />
            </label>
            {manage && (
              <>
                <button className="btn-ghost text-xs" onClick={() => runCleanup.mutate(true)}>
                  {t("runners.previewCleanup")}
                </button>
                <button
                  className="btn text-xs"
                  disabled={!cleanup || cleanup.preview === false || cleanup.snapshots === 0}
                  onClick={() => {
                    if (confirm(t("runners.confirmCleanup"))) runCleanup.mutate(false);
                  }}
                >
                  {t("runners.runCleanup")}
                </button>
              </>
            )}
          </div>
          <p className="muted text-xs">{t("runners.alwaysKeepLast")}</p>
          {cleanup && (
            /* Genannt wird der tatsaechlich frei werdende Platz, nicht die
               Summe der Snapshot-Groessen: ein Block gehoert keinem einzelnen
               Snapshot. Alles andere waere eine Zahl, die nie stimmt. */
            <p className="text-xs">
              {cleanup.preview
                ? t("runners.cleanupPreview", {
                    snapshots: cleanup.snapshots,
                    blocks: cleanup.blocks_removed,
                    freed: formatBytes(cleanup.freed_bytes),
                  })
                : t("runners.cleanupDone", {
                    snapshots: cleanup.snapshots,
                    blocks: cleanup.blocks_removed,
                    freed: formatBytes(cleanup.freed_bytes),
                  })}
            </p>
          )}
        </div>
      )}
    </div>
  );
}

// DiskBar zeigt den Fuellstand des Dateisystems, auf dem die Arbeitskopien
// liegen — genau die Zahl, die entscheidet, ob das naechste Home noch passt.
function DiskBar({ free, total }: { free: number; total: number }) {
  const used = total - free;
  const pct = Math.round((used / total) * 100);
  return (
    <div style={{ minWidth: 120 }}>
      <div
        style={{
          height: 6,
          borderRadius: 3,
          background: "var(--border)",
          overflow: "hidden",
        }}
      >
        <div
          style={{
            width: `${Math.min(100, pct)}%`,
            height: "100%",
            background: pct > 90 ? "var(--danger)" : pct > 75 ? "var(--warn, #b58900)" : "var(--accent)",
          }}
        />
      </div>
      <div className="muted text-xs">{formatBytes(free)} frei</div>
    </div>
  );
}
