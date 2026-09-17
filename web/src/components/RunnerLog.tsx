import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, post } from "../api";

type LogLine = {
  id: number;
  ts: string;
  level: string;
  msg: string;
  attrs?: Record<string, string>;
  agent_id?: string;
};

/* What a host says, where the host is managed.
 *
 * Before, it said it to its own stderr — under systemd that means the
 * journald of a machine on which someone has to have a shell. For exactly
 * the component that deliberately stands on a foreign machine, that was the
 * wrong place.
 *
 * Two levels, and they are not the same: what the runner SENDS is decided by
 * the switch above (it travels over the protocol). What is SHOWN here is
 * decided by the filter next to it. Confusing the two is the way to
 * put a host on debug and still see nothing. */
export default function RunnerLog({
  runnerId,
  level,
  connected,
  ships,
  manage,
}: {
  runnerId: string;
  level: string;
  connected: boolean;
  ships: boolean;
  manage: boolean;
}) {
  const { t, i18n } = useTranslation();
  const qc = useQueryClient();
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  const [show, setShow] = useState<"info" | "debug">("info");
  const [search, setSearch] = useState("");
  const [note, setNote] = useState("");
  // Lines about ONE start belong together. The filter sits on the row
  // itself, because that is where you need it — not in a field into which you
  // type a UUID.
  const [agent, setAgent] = useState<string | null>(null);
  // Show more instead of paging: you read a log from top to bottom and stop
  // when you have found what you are looking for. Pages with forward and
  // back would here be a control for a path nobody takes.
  const [limit, setLimit] = useState(200);

  const params = new URLSearchParams({ level: show, limit: String(limit) });
  if (search.trim()) params.set("q", search.trim());
  if (agent) params.set("agent", agent);

  const logs = useQuery({
    queryKey: ["runner-logs", runnerId, show, search.trim(), agent, limit],
    queryFn: () => api<LogLine[]>(`/runners/${runnerId}/logs?${params}`),
    refetchInterval: 5_000,
  });

  const setLevel = useMutation({
    mutationFn: (next: string) =>
      post<{ level: string; applied: boolean; error?: string }>(`/runners/${runnerId}/log-level`, { level: next }),
    onSuccess: (res) => {
      // Saved but not delivered is a state of its own: a host that is
      // currently gone takes it over on the next connection.
      // Keeping that quiet reads as "done".
      setNote(res.applied ? "" : t("runners.log.levelPending", { error: res.error ?? "" }));
      qc.invalidateQueries({ queryKey: ["runners"] });
    },
    onError: (e) => setNote(String((e as Error)?.message)),
  });

  const hhmmss = (iso: string) =>
    new Date(iso).toLocaleTimeString(locale, { hour12: false, hour: "2-digit", minute: "2-digit", second: "2-digit" });
  const day = (iso: string) => new Date(iso).toLocaleDateString(locale, { day: "2-digit", month: "long", year: "numeric" });
  const lines = logs.data ?? [];

  return (
    <section className="card mt-4">
      <h2 className="text-[15px]">{t("runners.log.title")}</h2>
      <p className="muted text-xs">{t("runners.log.hint")}</p>

      <div className="flex gap-3 items-end flex-wrap mb-3">
        {manage && (
          <div>
            <label>{t("runners.log.levelLabel")}</label>
            <div className="flex gap-1">
              {["info", "debug"].map((l) => (
                <button
                  key={l}
                  type="button"
                  className={"btn sm" + (level === l ? " primary" : "")}
                  disabled={setLevel.isPending || level === l}
                  onClick={() => setLevel.mutate(l)}
                >
                  {t(`runners.log.level_${l}`)}
                </button>
              ))}
            </div>
          </div>
        )}
        <div>
          <label>{t("runners.log.showLabel")}</label>
          <div className="flex gap-1">
            {(["info", "debug"] as const).map((l) => (
              <button
                key={l}
                type="button"
                className={"btn sm" + (show === l ? " primary" : "")}
                onClick={() => setShow(l)}
              >
                {t(`runners.log.level_${l}`)}
              </button>
            ))}
          </div>
        </div>
        <div className="flex-1 min-w-44">
          <label>{t("runners.log.searchLabel")}</label>
          <input value={search} onChange={(e) => setSearch(e.target.value)} placeholder={t("runners.log.searchPlaceholder")} />
        </div>
      </div>

      {!connected && <p className="muted text-xs">{t("runners.log.offline")}</p>}
      {!ships && <p className="warn-text text-xs">{t("runners.log.tooOld")}</p>}
      {level === "debug" && <p className="muted text-xs">{t("runners.log.debugOn")}</p>}
      {note && <p className="warn-text text-xs">{note}</p>}
      {agent && (
        <p className="text-xs mb-2">
          {t("runners.log.filteredByAgent", { agent: agent.slice(0, 8) })}{" "}
          <button className="rlog-clear" type="button" onClick={() => setAgent(null)}>
            {t("runners.log.clearFilter")}
          </button>
        </p>
      )}

      {logs.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
      {lines.length === 0 && ships && !logs.isLoading && (
        <p className="muted text-sm">{t("runners.log.empty")}</p>
      )}

      {lines.length > 0 && (
        <div className="rlog mono">
          {lines.map((l, i) => {
            // The list is newest first; the day change therefore belongs
            // BEFORE the first line of the respective day, that is where the
            // date changes compared to the previous line.
            const showDay = i === 0 || day(l.ts) !== day(lines[i - 1].ts);
            return (
              <div key={l.id}>
                {showDay && <div className="rlog-day">{day(l.ts)}</div>}
                <div className={`rlog-line lvl-${l.level}`}>
                  <span className="rlog-time">{hhmmss(l.ts)}</span>
                  <span className="rlog-level">{l.level}</span>
                  <span className="rlog-body">
                    <span className="rlog-msg">{l.msg}</span>
                    {l.agent_id && (
                      <button
                        type="button"
                        className="rlog-agent"
                        title={l.agent_id}
                        onClick={() => setAgent(agent === l.agent_id ? null : (l.agent_id ?? null))}
                      >
                        {t("runners.log.agentChip", { agent: l.agent_id.slice(0, 8) })}
                      </button>
                    )}
                    {l.attrs &&
                      Object.entries(l.attrs).map(([k, v]) => (
                        <span key={k} className="rlog-attr">
                          <span>{k}=</span>
                          {v}
                        </span>
                      ))}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {lines.length >= limit && limit < 1000 && (
        <button className="btn sm mt-2" type="button" onClick={() => setLimit(limit + 200)}>
          {t("runners.log.more")}
        </button>
      )}
      {lines.length >= 1000 && <p className="muted text-xs mt-2">{t("runners.log.capped")}</p>}
    </section>
  );
}
