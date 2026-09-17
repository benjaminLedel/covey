import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, patch, post, type Principal } from "../api";
import { fmtBytes } from "../format";

// The runners view (spec/16, stage 5). From the third runner on it is what
// makes operation workable: which hosts exist, which one carries right now,
// and where space runs low — before it is gone, not after.

type RunnerView = {
  id: string;
  kind: "builtin" | "remote";
  name: string;
  description?: string;
  tags?: string[];
  extra_tags?: string[];
  assigned_images?: string[];
  images_decided?: boolean;
  version?: string;
  arch?: string;
  protocol?: number;
  last_seen_at?: string;
  paused_at?: string;
  created_at: string;
  live?: {
    connected: boolean;
    protocol: number;
    version?: string;
    arch?: string;
    tags?: string[];
    images?: string[];
    reported_tags?: string[];
    reported_images?: string[];
    sandboxes: number;
    max_sandboxes?: number;
    outdated: boolean;
    // The line stands, the host says nothing. In this state it gets no
    // new sandboxes — and "connected" beside an agent that does not
    // start sends everyone in the wrong direction.
    unresponsive?: boolean;
  };
  capacity?: {
    sandboxes: number;
    total_bytes: number;
    free_bytes: number;
    work_dir?: string;
    // When the number came to be. The server asks the host in the background;
    // what stands here is the last thing heard — and a host that is pulling
    // an image does not answer for minutes.
    measured_at?: string;
  };
};

type StoreView = {
  enabled: boolean;
  bytes: number;
  logical_bytes: number;
  agents: number;
};

type CleanupView = {
  snapshots: number;
  blocks_removed: number;
  freed_bytes: number;
  preview: boolean;
};



/* embedded: see Runtimes — the tab carries the heading. */
export default function Runners({ me, embedded = false }: { me: Principal; embedded?: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  /* The same roles as in the server (httpapi: manage). This used to say
     "platform_admin" — the top org role has been called org_admin since
     migration 0061, and since no one carries it anymore, the answer was
     always no: the card for registering a runner was gone for everyone,
     also for those allowed to use the endpoint behind it. */
  const manage = me.Role === "org_admin" || me.Role === "agent_owner";
  const [token, setToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [cleanup, setCleanup] = useState<CleanupView | null>(null);

  const runners = useQuery({
    queryKey: ["runners"],
    queryFn: () => api<RunnerView[]>("/runners"),
    // The live part (connected, running sandboxes, free space) is only true
    // as long as the connection stands — hence refetch instead of fetching
    // once.
    refetchInterval: 10_000,
  });
  // What stands between this organisation's runners and a running sandbox.
  // The check existed before — it ran at startup into the log and into
  // the onboarding view, which disappears once the five steps are done.
  // That is exactly why an instance that had run for weeks reported
  // nothing when its data plane went down.
  const health = useQuery({
    queryKey: ["runner-health"],
    queryFn: () => api<{ ready: boolean; problems: string[] }>("/runners/health"),
    refetchInterval: 30_000,
  });
  const store = useQuery({
    queryKey: ["home-store"],
    queryFn: () => api<StoreView>("/platform/home-store"),
  });

  // Pause from the row. Same route as on the detail page —
  // it is the same decision, just closer by.
  const setPaused = useMutation({
    mutationFn: (v: { id: string; paused: boolean }) => patch(`/runners/${v.id}`, { paused: v.paused }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["runners"] }),
  });
  const createToken = useMutation({
    mutationFn: () => post<{ token: string }>("/runners/registration-tokens", {}),
    onSuccess: (r) => {
      setToken(r.token);
      qc.invalidateQueries({ queryKey: ["registration-tokens"] });
    },
  });
  // The tokens that exist, never the tokens themselves: a token is shown once
  // in the clear and lives on as a row — which is what makes taking it back
  // possible for somebody who was not there when it was created.
  const tokens = useQuery({
    queryKey: ["registration-tokens"],
    queryFn: () => api<RegistrationToken[]>("/runners/registration-tokens"),
    enabled: manage,
  });
  const revokeToken = useMutation({
    mutationFn: (id: string) => post<{ ok: boolean }>(`/runners/registration-tokens/${id}/revoke`, {}),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["registration-tokens"] }),
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
    <div className="flex flex-col gap-6">
      <div>
        {!embedded && <h1 className="text-[22px]">{t("runners.title")}</h1>}
        <p className="muted text-sm">{t("runners.intro")}</p>
      </div>

      {health.data && !health.data.ready && (
        <div className="card" role="status" style={{ borderColor: "var(--text-warning)" }}>
          <div className="font-medium">{t("runners.problemsTitle")}</div>
          <p className="muted text-sm">{t("runners.problemsIntro")}</p>
          <ul className="text-sm mono" style={{ marginTop: 8, paddingLeft: 18, listStyle: "disc" }}>
            {health.data.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
      )}

      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
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
                    <Link to={`/infrastructure/runners/${r.id}`}>
                      {r.name || (r.kind === "builtin" ? t("runners.builtin") : r.description || r.id.slice(0, 8))}
                    </Link>
                    {r.kind === "builtin" && (
                      <div className="muted text-xs">{t("runners.builtinHint")}</div>
                    )}
                    {r.capacity?.work_dir && <div className="muted text-xs mono">{r.capacity.work_dir}</div>}
                  </td>
                  <td>
                    {/* Paused stands before everything else: it is the only
                        state someone WANTED, and "connected" beside
                        an agent that is not running sends you in the wrong
                        direction. */}
                    {r.paused_at ? (
                      <>
                        <span className="pill mut">{t("runners.paused")}</span>
                        <div className="muted text-xs" style={{ marginTop: 2 }}>
                          {t("runners.pausedSince", { when: ago(r.paused_at, t) })}
                        </div>
                      </>
                    ) : r.live?.connected ? (
                      r.live.unresponsive ? (
                        <>
                          <span className="pill err">{t("runners.unresponsive")}</span>
                          <div className="muted text-xs" style={{ marginTop: 2 }}>
                            {t("runners.unresponsiveHint")}
                          </div>
                        </>
                      ) : (
                        <span className="pill ok">{t("runners.connected")}</span>
                      )
                    ) : (
                      <>
                        <span className="pill mut" title={t("runners.offlineHint")}>
                          {t("runners.offline")}
                        </span>
                        {/* For a runner that is gone, "since when" is the
                            actual information — a maintenance window reads
                            differently from a host not seen
                            for days. */}
                        {r.last_seen_at && (
                          <div className="muted text-xs" style={{ marginTop: 2 }}>
                            {t("runners.lastSeen", { when: ago(r.last_seen_at, t) })}
                          </div>
                        )}
                      </>
                    )}
                    {/* Version drift is named, not merely tolerated:
                        runner and server are shipped separately. */}
                    {r.live?.outdated && (
                      <span className="pill err" style={{ marginLeft: 6 }}>
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
                  {/* The running sandboxes the connection knows first-hand and
                      exactly; the disk is what the host last reported.
                      Hence not the number from the disk
                      report here: that would be one beat old. */}
                  <td>
                    {r.live
                      ? r.live.max_sandboxes
                        ? t("runners.sandboxesOf", { running: r.live.sandboxes, max: r.live.max_sandboxes })
                        : t("runners.sandboxes", { count: r.live.sandboxes })
                      : "—"}
                  </td>
                  <td>
                    {r.capacity && r.capacity.total_bytes > 0 ? (
                      <>
                        <DiskBar free={r.capacity.free_bytes} total={r.capacity.total_bytes} />
                        {/* Only once the number is noticeably old does its age
                            become information: a host that has said
                            nothing for twenty minutes may long since have less space.
                            While it is fresh, the line would be noise. */}
                        {stale(r.capacity.measured_at) && (
                          <div className="muted text-xs" style={{ marginTop: 2 }}>
                            {t("runners.diskAsOf", { when: ago(r.capacity.measured_at!, t) })}
                          </div>
                        )}
                      </>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td className="text-xs mono">{r.live?.version || r.version || "—"}</td>
                  <td className="text-right">
                    {/* The name is also a link, but a link in a
                        table cell looks like text: "Edit" stands
                        where every other row has its action.
                        The built-in runner has it too — name, tags, and
                        workplaces apply to it just the same; what it lacks
                        is deletion. */}
                    {manage && (
                      <div className="flex gap-2 justify-end">
                        {/* Pause is the action you want from the
                            overview: you see here which host
                            carries and which sticks, and the handle for it must
                            not sit one page further away. Everything else —
                            names, tags, workplaces — needs room for
                            a sentence beside it and stays above. */}
                        <button
                          className="btn-ghost text-xs"
                          disabled={setPaused.isPending}
                          onClick={() => setPaused.mutate({ id: r.id, paused: !r.paused_at })}
                        >
                          {r.paused_at ? t("runners.detail.resume") : t("runners.detail.pause")}
                        </button>
                        <Link className="btn sm" to={`/infrastructure/runners/${r.id}`}>
                          {t("runners.edit")}
                        </Link>
                      </div>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {manage && (
        <div className="card p-4 flex flex-col gap-3">
          <h2 className="text-[15px]">{t("runners.addTitle")}</h2>
          <p className="muted text-xs">{t("runners.addHint")}</p>
          <div>
            <button className="btn" onClick={() => createToken.mutate()} disabled={createToken.isPending}>
              {t("runners.newToken")}
            </button>
          </div>
          {token && (
            <div className="flex flex-col gap-2" style={{ marginTop: 4 }}>
              {/* Once in plaintext, afterwards only as a hash — hence for
                  copying and not for typing. The command is built so
                  that it can be pasted: no placeholder someone
                  accidentally carries along. */}
              <p className="text-xs">{t("runners.tokenOnce")}</p>
              <pre className="console">
                {registerCommand(token)
                  .split("\n")
                  .map((line) => (
                    <span className="line" key={line}>
                      {line}
                    </span>
                  ))}
                <button
                  className="copy"
                  onClick={() => {
                    navigator.clipboard.writeText(registerCommand(token)).then(() => {
                      setCopied(true);
                      setTimeout(() => setCopied(false), 1500);
                    });
                  }}
                >
                  {copied ? t("runners.copied") : t("runners.copy")}
                </button>
              </pre>
            </div>
          )}
          {tokens.data && tokens.data.length > 0 && (
            <table className="text-sm" style={{ marginTop: 8 }}>
              <thead>
                <tr>
                  <th>{t("runners.tokenCreated")}</th>
                  <th>{t("runners.tokenState")}</th>
                  <th></th>
                </tr>
              </thead>
              <tbody>
                {tokens.data.map((tok) => {
                  const state = tok.revoked_at
                    ? t("runners.tokenRevoked")
                    : new Date(tok.expires_at).getTime() < Date.now()
                      ? t("runners.tokenExpired")
                      : t("runners.tokenUsableUntil", { until: new Date(tok.expires_at).toLocaleString() });
                  const usable = !tok.revoked_at && new Date(tok.expires_at).getTime() >= Date.now();
                  return (
                    <tr key={tok.id}>
                      <td>{new Date(tok.created_at).toLocaleString()}</td>
                      <td className={usable ? "" : "muted"}>{state}</td>
                      <td>
                        {usable && (
                          <button className="btn btn-ghost" onClick={() => revokeToken.mutate(tok.id)}>
                            {t("runners.revokeToken")}
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          )}
        </div>
      )}

      {store.data?.enabled && (
        <div className="card p-4 flex flex-col gap-3">
          <h2 className="text-[15px]">{t("runners.storeTitle")}</h2>
          <p className="muted text-xs">{t("runners.storeHint")}</p>
          <div className="text-sm" style={{ marginTop: 4 }}>
            {t("runners.storeSize", {
              size: fmtBytes(store.data.bytes),
              agents: store.data.agents,
            })}
          </div>
          {/* The comparison is the explanation: the homes together weigh a
              multiple of what the store occupies, because the toolchain caches
              are byte-for-byte identical on every developer home and therefore
              lie there once. Without this line the store is a
              directory that grows for invisible reasons. */}
          {store.data.logical_bytes > 0 && store.data.bytes > 0 && (
            <div className="text-sm">
              {/* Below 1.1×, "x times smaller" is silly — and the store even
                  sits slightly above that, because the manifests themselves
                  are blocks. Then the sentence says when the saving arrives,
                  instead of claiming one that does not exist. */}
              {store.data.logical_bytes / store.data.bytes >= 1.1
                ? t("runners.storeDedup", {
                    logical: fmtBytes(store.data.logical_bytes),
                    factor: (store.data.logical_bytes / store.data.bytes).toFixed(1),
                  })
                : t("runners.storeNoDedupYet", { logical: fmtBytes(store.data.logical_bytes) })}
            </div>
          )}

          <div className="flex items-center gap-4 flex-wrap" style={{ marginTop: 4 }}>
            {manage && (
              <>
                <button className="btn-ghost text-xs" onClick={() => runCleanup.mutate(true)}>
                  {t("runners.previewCleanup")}
                </button>
                <button
                  className="btn text-xs"
                  disabled={!cleanup || cleanup.preview === false || cleanup.blocks_removed === 0}
                  onClick={() => {
                    if (confirm(t("runners.confirmCleanup"))) runCleanup.mutate(false);
                  }}
                >
                  {t("runners.runCleanup")}
                </button>
              </>
            )}
          </div>
          {cleanup && (
            /* What is named is the space actually freed, not the size of what
               is being removed: a block belongs to no single home. Anything
               else would be a number that is never right. */
            <p className="text-xs">
              {cleanup.preview
                ? t("runners.cleanupPreview", {
                    blocks: cleanup.blocks_removed,
                    freed: fmtBytes(cleanup.freed_bytes),
                  })
                : t("runners.cleanupDone", {
                    blocks: cleanup.blocks_removed,
                    freed: fmtBytes(cleanup.freed_bytes),
                  })}
            </p>
          )}
          <p className="muted text-xs">{t("runners.cleanupAutomatic")}</p>
        </div>
      )}
    </div>
  );
}

// registerCommand is what has to run on the new host — to paste as a whole,
// and from the point where this host truly stands: without
// covey-runner. A command that presupposes a binary that is not there is
// one you first still have to go looking for.
//
// The install script comes from this instance and brings the version that
// matches it (spec/16, "protocol version") — hence its own address
// and not GitHub's.
//
// Without description and tags: both are optional, and a placeholder in the
// command is something someone copies along and then wonders why their
// runner is called "…".
type RegistrationToken = {
  id: string;
  description: string;
  created_at: string;
  expires_at: string;
  revoked_at?: string;
};

function registerCommand(token: string): string {
  const origin = window.location.origin;
  return [
    `curl -fsSL ${origin}/install.sh | sh -s -- --runner`,
    `covey-runner register --url ${origin} --token ${token}`,
    `covey-runner run`,
  ].join("\n");
}

// ago is "X ago" in coarse rounding. Finer would be useless: for a runner
// that is gone, the order of magnitude decides — minutes are a restart,
// days are a host no one takes care of anymore.
// stale: from which point a number's age is itself information. The server
// asks again on the heartbeat cadence (30s); two minutes without a new
// number means the host is not answering — not that the page is slow.
function stale(iso?: string): boolean {
  if (!iso) return false;
  return Date.now() - new Date(iso).getTime() > 120_000;
}

function ago(iso: string, t: (k: string, o?: Record<string, unknown>) => string): string {
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 90) return t("runners.agoJustNow");
  if (seconds < 3600) return t("runners.agoMinutes", { count: Math.round(seconds / 60) });
  if (seconds < 86400) return t("runners.agoHours", { count: Math.round(seconds / 3600) });
  return t("runners.agoDays", { count: Math.round(seconds / 86400) });
}

// DiskBar shows the fill level of the filesystem the working copies live
// on — exactly the number that decides whether the next home still fits.
function DiskBar({ free, total }: { free: number; total: number }) {
  const { t } = useTranslation();
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
      <div className="muted text-xs">{t("runners.free", { size: fmtBytes(free) })}</div>
    </div>
  );
}
