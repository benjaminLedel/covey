import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import i18n from "../../i18n";
import {
  api,
  post,
  type HeartbeatStatus,
  type Task,
} from "../../api";
import { fmtDelta } from "../../format";

// The schedule in the reader's time.
//
// daily_at is a bare clock time in the server's timezone, next_run an absolute
// point in time. The card showed both side by side — `taeglich um 03:00` and
// next to it `(Do 05:00)` —, and above it stood that the times were server
// time. Two clock times for the same run, a heading that only holds for one
// of them: that reads like an error in the plan.
//
// Because next_run is the same run, just absolute, the local time can be
// derived from it. The server value stays on as title — whoever needs it (it
// stands like that in HEARTBEAT.md), finds it there.
function scheduleLabel(hb: HeartbeatStatus): string {
  if (hb.every_seconds) return i18n.t("agent.heartbeat.schedule_interval", { delta: fmtDelta(hb.every_seconds * 1000) });
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const next = new Date(hb.next_run);
  const time = isNaN(next.getTime())
    ? (hb.daily_at ?? "")
    : next.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  return i18n.t("agent.heartbeat.schedule_daily", { time });
}

function fmtRunChip(d: Date): string {
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const time = d.toLocaleTimeString(locale, { hour: "2-digit", minute: "2-digit" });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString(locale, { weekday: "short" })} ${time}`;
}

function upcomingRuns(hb: HeartbeatStatus, horizonMs: number): Date[] {
  const now = Date.now();
  const step = (hb.every_seconds ?? 24 * 3600) * 1000;
  let t = new Date(hb.next_run).getTime();
  const runs: Date[] = [];
  if (t <= now) {
    runs.push(new Date(now));
    while (t <= now) t += step;
  }
  while (t <= now + horizonMs && runs.length < 48) {
    runs.push(new Date(t));
    t += step;
  }
  return runs;
}

function HeartbeatTimeline({ runs, horizonMs }: { runs: Date[]; horizonMs: number }) {
  const { t } = useTranslation();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const now = Date.now();
  return (
    <div>
      <div style={{ position: "relative", height: 14 }}>
        <div
          style={{
            position: "absolute",
            top: 6,
            left: 0,
            right: 0,
            height: 2,
            background: "var(--border)",
            borderRadius: 1,
          }}
        />
        {runs.map((r, i) => (
          <span
            key={i}
            title={r.toLocaleString(locale)}
            style={{
              position: "absolute",
              top: 3,
              left: `calc(${Math.min(99, Math.max(0, ((r.getTime() - now) / horizonMs) * 100))}% - 4px)`,
              width: 8,
              height: 8,
              borderRadius: "50%",
              background: "var(--text-accent)",
            }}
          />
        ))}
      </div>
      <div className="flex muted" style={{ fontSize: 10, justifyContent: "space-between" }}>
        <span>{t("agent.heartbeat.now")}</span>
        <span>+{Math.round(horizonMs / 3600000)} h</span>
      </div>
    </div>
  );
}

function HeartbeatCard({
  hb,
  horizonMs,
  agentId,
  canManage,
  killed,
}: {
  hb: HeartbeatStatus;
  horizonMs: number;
  agentId: string;
  canManage: boolean;
  killed: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const fire = useMutation({
    mutationFn: () => post<Task>(`/agents/${agentId}/heartbeats/${encodeURIComponent(hb.name)}/fire`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
      qc.invalidateQueries({ queryKey: ["backlog", agentId] });
    },
  });
  const runs = upcomingRuns(hb, horizonMs);
  const next = new Date(hb.next_run);
  const overdue = next.getTime() <= Date.now();
  return (
    <div className="card mb-4">
      <div className="flex items-center gap-2 mb-2 flex-wrap">
        <span className="font-medium">{hb.name}</span>
        <span className="badge" title={hb.daily_at ? t("agent.heartbeat.serverTime", { time: hb.daily_at }) : undefined}>
          {scheduleLabel(hb)}
        </span>
        {killed && (
          <span className="badge state st-killed" title={t("agent.heartbeat.stoppedHint")}>
            {t("agent.heartbeat.stopped")}
          </span>
        )}
        {hb.source === "system" && (
          <span className="badge st-pro" title={t("agent.heartbeat.systemHint")}>
            {t("agent.heartbeat.system")}
          </span>
        )}
        {hb.only_if && (
          <span className="badge" title={t("agent.heartbeat.onlyIfHint", { system: hb.only_if })}>
            {t("agent.heartbeat.onlyIf", { system: hb.only_if })}
          </span>
        )}
        {hb.pending && (
          <span className="badge st-blocked" title={t("agent.heartbeat.taskOpenHint")}>
            {t("agent.heartbeat.taskOpen")}
          </span>
        )}
        <span className="ml-auto muted text-xs">
          {t("agent.heartbeat.lastRun", { delta: fmtDelta(Date.now() - new Date(hb.last_fired_at).getTime()) })}
        </span>
        {canManage && (
          <button
            className="btn sm"
            disabled={fire.isPending || hb.pending}
            title={hb.pending ? t("agent.heartbeat.firePendingHint") : t("agent.heartbeat.fireHint")}
            onClick={() => fire.mutate()}
          >
            {fire.isPending ? t("agent.heartbeat.running") : t("agent.heartbeat.fireNow")}
          </button>
        )}
      </div>
      {fire.isError && (
        <p className="text-xs mb-2" style={{ color: "var(--text-danger)" }}>
          {(fire.error as Error).message}
        </p>
      )}
      <p className="muted text-xs mb-2" style={{ maxWidth: 680 }}>
        {hb.task}
      </p>
      {/* For a stopped agent the scheduler does NOT fire (the query
          in orchestrator.go filters `WHERE NOT a.killed`). The card still
          claimed "overdue — the next tick will create the task", thus
          work that will not happen. Whoever checks on a stalled agent
          reads that as "it is running" and looks elsewhere for the cause. */}
      <p className="text-xs font-medium mb-2">
        {killed
          ? t("agent.heartbeat.pausedWhileStopped")
          : overdue
          ? hb.pending
            ? t("agent.heartbeat.overdueWithPending")
            : t("agent.heartbeat.overdue")
          : t("agent.heartbeat.nextRun", { delta: fmtDelta(next.getTime() - Date.now()) })}
        {!killed && runs.length > 0 && (
          <span className="muted font-normal">
            {" "}
            ({runs.slice(0, 3).map(fmtRunChip).join(" · ")}
            {runs.length > 3 ? " · …" : ""})
          </span>
        )}
      </p>
      {!killed && <HeartbeatTimeline runs={runs} horizonMs={horizonMs} />}
    </div>
  );
}

export function Heartbeats({
  agentId,
  canManage,
  killed,
}: {
  agentId: string;
  canManage: boolean;
  killed: boolean;
}) {
  const { t } = useTranslation();
  const horizonMs = 24 * 3600 * 1000;
  const hbs = useQuery({
    queryKey: ["heartbeats", agentId],
    queryFn: () => api<HeartbeatStatus[]>(`/agents/${agentId}/heartbeats`),
    refetchInterval: 15000,
  });
  const list = hbs.data ?? [];
  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.heartbeat.desc")}
      </p>
      {/* While the query was running, null was rendered — calling
          ?tab=einstellungen&sub=heartbeat directly thus showed an
          empty page, and only a click to another sub-item and
          back brought the content. A loading state does not look like an
          error; rendering nothing does. */}
      {hbs.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
      {!hbs.isLoading && list.length === 0 && (
        <div className="kc-empty">
          {t("agent.heartbeat.noHeartbeats")}
        </div>
      )}
      {list.map((hb) => (
        <HeartbeatCard
          key={hb.name}
          hb={hb}
          horizonMs={horizonMs}
          agentId={agentId}
          canManage={canManage}
          killed={killed}
        />
      ))}
    </div>
  );
}
