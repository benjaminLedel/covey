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

function scheduleLabel(hb: HeartbeatStatus): string {
  if (hb.every_seconds) return i18n.t("agent.heartbeat.schedule_interval", { delta: fmtDelta(hb.every_seconds * 1000) });
  return i18n.t("agent.heartbeat.schedule_daily", { time: hb.daily_at });
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
}: {
  hb: HeartbeatStatus;
  horizonMs: number;
  agentId: string;
  canManage: boolean;
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
        <span className="badge">{scheduleLabel(hb)}</span>
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
      <p className="text-xs font-medium mb-2">
        {overdue
          ? hb.pending
            ? t("agent.heartbeat.overdueWithPending")
            : t("agent.heartbeat.overdue")
          : t("agent.heartbeat.nextRun", { delta: fmtDelta(next.getTime() - Date.now()) })}
        {runs.length > 0 && (
          <span className="muted font-normal">
            {" "}
            ({runs.slice(0, 3).map(fmtRunChip).join(" · ")}
            {runs.length > 3 ? " · …" : ""})
          </span>
        )}
      </p>
      <HeartbeatTimeline runs={runs} horizonMs={horizonMs} />
    </div>
  );
}

export function Heartbeats({ agentId, canManage }: { agentId: string; canManage: boolean }) {
  const { t } = useTranslation();
  const horizonMs = 24 * 3600 * 1000;
  const hbs = useQuery({
    queryKey: ["heartbeats", agentId],
    queryFn: () => api<HeartbeatStatus[]>(`/agents/${agentId}/heartbeats`),
    refetchInterval: 15000,
  });
  if (hbs.isLoading) return null;
  const list = hbs.data ?? [];
  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.heartbeat.desc")}
      </p>
      {list.length === 0 && (
        <div className="kc-empty">
          {t("agent.heartbeat.noHeartbeats")}
        </div>
      )}
      {list.map((hb) => (
        <HeartbeatCard key={hb.name} hb={hb} horizonMs={horizonMs} agentId={agentId} canManage={canManage} />
      ))}
    </div>
  );
}
