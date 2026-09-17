import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { api, patch, post, del, type Principal } from "../api";
import RunnerLog from "../components/RunnerLog";

/* A host's detail page.
 *
 * The fields used to sit in the table row, and that was the wrong place:
 * what a runner can do is a decision with a rationale — a tag excludes
 * agents, a workplace list says something about cost. Both need a
 * sentence beside them, and a sentence does not fit in a column next to
 * load and disk space.
 *
 * The overview stays the overview: which hosts exist, which one carries,
 * where space runs tight. Whoever wants to change something clicks the name. */

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
  log_level?: string;
  live?: {
    connected: boolean;
    protocol?: number;
    version?: string;
    arch?: string;
    tags?: string[];
    images?: string[];
    reported_tags?: string[];
    reported_images?: string[];
    sandboxes: number;
    max_sandboxes?: number;
    features?: string[];
    outdated: boolean;
    unresponsive?: boolean;
  };
  capacity?: {
    sandboxes: number;
    max_sandboxes?: number;
    total_bytes: number;
    free_bytes: number;
    work_dir?: string;
    measured_at?: string;
  };
};

const list = (v?: string[]) => (v ?? []).join(", ");
const parse = (v: string) =>
  v
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);

export default function RunnerDetail({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [sp, setSp] = useSearchParams();
  const manage = me.Role === "org_admin" || me.Role === "agent_owner";

  const runners = useQuery({
    queryKey: ["runners"],
    queryFn: () => api<RunnerView[]>("/runners"),
    refetchInterval: 10_000,
  });
  const r = (runners.data ?? []).find((x) => x.id === id);

  const [error, setError] = useState("");
  // Workplaces to pre-pull: what this organisation knows, plus a free-text
  // field for a reference that stands in no catalogue.
  const workplaces = useQuery({
    queryKey: ["workplaces"],
    queryFn: () => api<{ name: string; label: string; image: string }[]>("/workplaces"),
  });
  const [ownImage, setOwnImage] = useState("");
  const [pullResult, setPullResult] = useState<{ image: string; ok: boolean; error?: string } | null>(null);
  const pull = useMutation({
    mutationFn: (body: { workplace?: string; image?: string }) =>
      post<{ image: string; ok: boolean; error?: string }>(`/runners/${id}/pull`, body),
    onSuccess: (res) => {
      setPullResult(res);
      qc.invalidateQueries({ queryKey: ["runners"] });
    },
    onError: (e) => setPullResult({ image: "", ok: false, error: String((e as Error)?.message) }),
  });
  // "Update" is an action with a result, not a switch: the host pulls its
  // new binary, reports what came of it, and then goes away briefly.
  const [updateResult, setUpdateResult] = useState<{
    ok: boolean;
    error?: string;
    from?: string;
    to?: string;
    restarting?: boolean;
    // planned: The host was carrying sandboxes. That is not a failure,
    // but a "not now" — the control plane catches up in the next
    // gap.
    planned?: boolean;
  } | null>(null);
  const update = useMutation({
    mutationFn: () => post<{ ok: boolean; error?: string; from?: string; to?: string; restarting?: boolean; planned?: boolean }>(
      `/runners/${id}/update`,
      {},
    ),
    onSuccess: (res) => {
      setUpdateResult(res);
      qc.invalidateQueries({ queryKey: ["runners"] });
    },
    onError: (e) => setUpdateResult({ ok: false, error: String((e as Error)?.message) }),
  });
  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch(`/runners/${id}`, body),
    onSuccess: () => {
      setError("");
      qc.invalidateQueries({ queryKey: ["runners"] });
    },
    onError: (e) => setError(String((e as Error)?.message)),
  });
  const remove = useMutation({
    mutationFn: () => del(`/runners/${id}`),
    onSuccess: () => navigate("/infrastructure/runners"),
  });

  if (runners.isLoading) return <p className="muted text-sm">{t("common.loading")}</p>;
  if (!r) return <p className="muted text-sm">{t("runners.detail.gone")}</p>;

  const connected = !!r.live?.connected;

  /* Five areas instead of nine cards stacked.
   *
   * The page was a scroll: identity, tags, images, pre-pull, update,
   * pause, log, state, remove — and whoever wanted to see what the host
   * is saying right now scrolled past everything they do not want to
   * change today. The menu is the same as in the agent
   * settings (settings-panes/settings-nav), so no one has to learn two
   * patterns for the same thing.
   *
   * The sub-item stands in the URL: a link to a host's log is exactly
   * what you drop into a chat when something sticks. */
  const subs = [
    ["identity", t("runners.detail.subIdentity"), true],
    ["workplaces", t("runners.detail.subWorkplaces"), true],
    ["log", t("runners.detail.subLog"), true],
    ["state", t("runners.detail.subState"), true],
    ["maintenance", t("runners.detail.subMaintenance"), manage],
  ] as const;
  // State sits further down: version, architecture and protocol change
  // almost never, and whatever of them interests daily already stands in
  // the overview table. Up top belongs what you change or read.
  const wanted = sp.get("sub") ?? "identity";
  const sub = subs.some(([k, , allowed]) => k === wanted && allowed) ? wanted : "identity";
  const setSub = (key: string) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        n.set("sub", key);
        return n;
      },
      { replace: false },
    );

  return (
    <div className="rd-page">
      <div>
        <Link to="/infrastructure/runners" className="muted text-sm">
          ← {t("runners.detail.back")}
        </Link>
        <div className="flex items-baseline gap-3 mt-1">
          <h1 className="text-[22px]">
            {r.name || (r.kind === "builtin" ? t("runners.builtin") : r.description || r.id.slice(0, 8))}
          </h1>
          <span
            className={
              r.paused_at ? "pill mut" : connected ? (r.live?.unresponsive ? "pill err" : "pill ok") : "pill mut"
            }
          >
            {r.paused_at
              ? t("runners.paused")
              : connected
                ? r.live?.unresponsive
                  ? t("runners.unresponsive")
                  : t("runners.connected")
                : t("runners.offline")}
          </span>
          {r.live?.outdated && <span className="pill err">{t("runners.outdated")}</span>}
        </div>
        <p className="muted text-sm mono">{r.id}</p>
      </div>

      {error && <div className="card danger-text text-sm">{error}</div>}

      <div className="settings-panes">
        <nav className="settings-nav" role="tablist">
          {subs
            .filter(([, , allowed]) => allowed)
            .map(([key, label]) => (
              <button
                key={key}
                role="tab"
                aria-selected={sub === key}
                className={`nav-item${sub === key ? " active" : ""}`}
                onClick={() => setSub(key)}
              >
                {label}
              </button>
            ))}
        </nav>

        <div className="rd-panes-content">
          {sub === "state" && (
            <div className="card rd-card">
              <h2 className="text-[15px]">{t("runners.detail.state")}</h2>
              <Row label={t("runners.colVersion")} value={r.live?.version || r.version || "—"} />
              <Row label={t("runners.detail.arch")} value={r.live?.arch || r.arch || "—"} />
              {/* The row knows the protocol only once the runner has reported
                  in — for the built-in it stands only in the connection. */}
              <Row
                label={t("runners.detail.protocol")}
                value={String(r.live?.protocol || r.protocol || "—")}
              />
              <Row
                label={t("runners.detail.sandboxes")}
                value={(() => {
                  const running = r.live?.sandboxes ?? r.capacity?.sandboxes ?? 0;
                  // The host reports its own limit — it stands in its configuration
                  // and not here. "3 of 4" answers the question you ask of this
                  // row; "3" answers it only halfway.
                  const max = r.live?.max_sandboxes ?? r.capacity?.max_sandboxes ?? 0;
                  return max > 0 ? t("runners.detail.sandboxesOf", { running, max }) : String(running);
                })()}
              />
              <Row label={t("runners.detail.workDir")} value={r.capacity?.work_dir || "—"} />
              <Row
                label={t("runners.detail.lastSeen")}
                value={r.last_seen_at?.replace("T", " ").slice(0, 19) || "—"}
              />
            </div>
          )}

          {sub === "identity" && (
            <>
              <div className="card rd-card">
                <h2 className="text-[15px]">{t("runners.detail.identity")}</h2>
                <p className="muted text-xs">{t("runners.detail.identityHint")}</p>
                <Field
                  label={t("runners.detail.name")}
                  value={r.name ?? ""}
                  placeholder={r.kind === "builtin" ? t("runners.builtin") : r.description || r.id.slice(0, 8)}
                  disabled={!manage}
                  onSave={(v) => save.mutate({ name: v })}
                />
                <Field
                  label={t("runners.detail.description")}
                  value={r.description ?? ""}
                  placeholder={t("runners.detail.descriptionPlaceholder")}
                  disabled={!manage}
                  onSave={(v) => save.mutate({ description: v })}
                />
              </div>

              <div className="card rd-card">
                <h2 className="text-[15px]">{t("runners.detail.steering")}</h2>
                <p className="muted text-xs">{t("runners.detail.steeringHint")}</p>
                <Field
                  label={t("runners.tagsLabel")}
                  value={list(r.extra_tags)}
                  placeholder={t("runners.tagsPlaceholder")}
                  disabled={!manage}
                  onSave={(v) => save.mutate({ tags: parse(v) })}
                />
                <Reported label={t("runners.detail.reportedTags")} value={list(r.live?.reported_tags ?? r.tags)} />
                <Reported label={t("runners.detail.effectiveTags")} value={list(r.live?.tags ?? r.tags)} />
              </div>
            </>
          )}

          {sub === "workplaces" && (
            <>
              <div className="card rd-card">
                <h2 className="text-[15px]">{t("runners.imagesLabel")}</h2>
                <p className="muted text-xs">{t("runners.detail.imagesHint")}</p>
                <Field
                  label={t("runners.detail.assignedImages")}
                  value={list(r.assigned_images)}
                  placeholder={t("runners.imagesPlaceholder")}
                  disabled={!manage}
                  onSave={(v) => save.mutate({ images: parse(v) })}
                  mono
                />
                <Reported
                  label={t("runners.detail.reportedImages")}
                  value={list(r.live?.reported_images) || t("runners.imagesNone")}
                />
              </div>

              {manage && (
                <div className="card rd-card">
                  <h2 className="text-[15px]">{t("runners.detail.pullTitle")}</h2>
                  <p className="muted text-xs">{t("runners.detail.pullHint")}</p>
                  {!connected && <p className="muted text-sm">{t("runners.detail.pullOffline")}</p>}
                  <div className="flex flex-wrap gap-2">
                    {(workplaces.data ?? []).map((w) => (
                      <button
                        key={w.name}
                        className="btn sm"
                        disabled={!connected || pull.isPending}
                        onClick={() => {
                          setPullResult(null);
                          pull.mutate({ workplace: w.name });
                        }}
                        title={w.image}
                      >
                        {w.label || w.name}
                      </button>
                    ))}
                  </div>
                  <form
                    className="flex gap-2 items-center"
                    onSubmit={(e) => {
                      e.preventDefault();
                      if (!ownImage.trim()) return;
                      setPullResult(null);
                      pull.mutate({ image: ownImage.trim() });
                    }}
                  >
                    <input
                      className="mono text-sm"
                      style={{ flex: 1 }}
                      placeholder={t("runners.detail.pullPlaceholder")}
                      value={ownImage}
                      onChange={(e) => setOwnImage(e.target.value)}
                    />
                    <button className="btn sm" disabled={!connected || pull.isPending}>
                      {t("runners.detail.pull")}
                    </button>
                  </form>
                  {pull.isPending && <p className="muted text-sm">{t("runners.detail.pullRunning")}</p>}
                  {pullResult && !pull.isPending && (
                    <p className={pullResult.ok ? "text-sm" : "text-sm danger-text"}>
                      {pullResult.ok
                        ? t("runners.detail.pullDone", { image: pullResult.image })
                        : t("runners.detail.pullFailed", { image: pullResult.image, error: pullResult.error })}
                    </p>
                  )}
                </div>
              )}
            </>
          )}

          {sub === "log" && (
            <RunnerLog
              runnerId={r.id}
              level={r.log_level || "info"}
              connected={connected}
              // A host whose build does not ship logs yet is not silent —
              // it cannot. Telling them apart is the difference between
              // "nothing going on" and "you never see anything here".
              ships={!connected || (r.live?.features ?? []).includes("log_shipping")}
              manage={manage}
            />
          )}

          {sub === "maintenance" && manage && (
            <>
              {r.kind === "remote" && (
                <div className="card rd-card">
                  <h2 className="text-[15px]">{t("runners.detail.updateTitle")}</h2>
                  <p className="muted text-xs">{t("runners.detail.updateHint")}</p>
                  <Reported label={t("runners.colVersion")} value={r.live?.version || r.version || "—"} />
                  {!connected && <p className="muted text-sm">{t("runners.detail.updateOffline")}</p>}
                  <div>
                    <button
                      className="btn sm"
                      disabled={!connected || update.isPending}
                      onClick={() => {
                        setUpdateResult(null);
                        update.mutate();
                      }}
                    >
                      {t("runners.detail.update")}
                    </button>
                  </div>
                  {update.isPending && <p className="muted text-sm">{t("runners.detail.updateRunning")}</p>}
                  {updateResult && !update.isPending && (
                    <p className={updateResult.ok || updateResult.planned ? "text-sm" : "text-sm danger-text"}>
                      {updateResult.planned
                        ? t("runners.detail.updatePlanned", { to: updateResult.to })
                        : !updateResult.ok
                        ? t("runners.detail.updateFailed", { error: updateResult.error })
                        : updateResult.restarting
                          ? t("runners.detail.updateDone", { from: updateResult.from, to: updateResult.to })
                          : t("runners.detail.updateCurrent", { version: updateResult.to || updateResult.from })}
                    </p>
                  )}
                </div>
              )}

              <div className="card rd-card">
                <h2 className="text-[15px]">{t("runners.detail.pauseTitle")}</h2>
                {/* The text depends on state because the question is: "what happens
                    when I press this" is a different one for a paused host than
                    for a running one. */}
                <p className="muted text-xs">
                  {r.paused_at ? t("runners.detail.pausedHint") : t("runners.detail.pauseHint")}
                </p>
                <div>
                  <button
                    className="btn sm"
                    disabled={save.isPending}
                    onClick={() => save.mutate({ paused: !r.paused_at })}
                  >
                    {r.paused_at ? t("runners.detail.resume") : t("runners.detail.pause")}
                  </button>
                </div>
              </div>

              {r.kind === "remote" && (
                <div className="card rd-card">
                  <h2 className="text-[15px]">{t("runners.detail.removeTitle")}</h2>
                  <p className="muted text-xs">{t("runners.detail.removeHint")}</p>
                  <div>
                    <button
                      className="btn-ghost danger-text text-sm"
                      onClick={() => {
                        if (confirm(t("runners.confirmDelete"))) remove.mutate();
                      }}
                    >
                      {t("runners.detail.remove")}
                    </button>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

/* A field that saves on leaving — the same mechanism as in the agent
 * settings, so no one searches for a save button that does not exist
 * at the other place.
 *
 * Layout deliberately as everywhere else: <label> above the field, not beside.
 * `label { display: block }` stands unlayered in styles.css and beats
 * every Tailwind display utility — a className="flex" on a <label> does
 * nothing here, and you only see that in the browser. */
function Field({
  label,
  value,
  placeholder,
  disabled,
  mono,
  onSave,
}: {
  label: string;
  value: string;
  placeholder?: string;
  disabled?: boolean;
  mono?: boolean;
  onSave: (v: string) => void;
}) {
  return (
    <div className="rd-field">
      <label>{label}</label>
      <input
        className={mono ? "mono" : undefined}
        key={`${label}:${value}`}
        defaultValue={value}
        placeholder={placeholder}
        disabled={disabled}
        onBlur={(e) => {
          const next = e.target.value.trim();
          if (next !== value) onSave(next);
        }}
      />
    </div>
  );
}

// What the host reports about itself stands next to what someone assigned —
// small and quiet: it is information, not an input field.
function Reported({ label, value }: { label: string; value: string }) {
  return (
    <div className="rd-reported">
      <span>{label}</span>
      <span className="mono">{value || "—"}</span>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="rd-row">
      <span className="muted">{label}</span>
      <span className="mono">{value}</span>
    </div>
  );
}
