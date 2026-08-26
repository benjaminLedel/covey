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

/* Was ein Host sagt, dort wo der Host verwaltet wird.
 *
 * Vorher sagte er es seinem eigenen stderr — unter systemd also ins journald
 * einer Maschine, auf die jemand eine Shell haben muss. Fuer ausgerechnet die
 * Komponente, die absichtlich auf einer fremden Maschine steht, war das der
 * falsche Ort.
 *
 * Zwei Pegel, und sie sind nicht dasselbe: Was der Runner SCHICKT, entscheidet
 * der Schalter oben (er reist ueber das Protokoll). Was hier ANGEZEIGT wird,
 * entscheidet der Filter daneben. Die beiden zu verwechseln ist der Weg,
 * einen Host auf debug zu stellen und trotzdem nichts zu sehen. */
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

  const params = new URLSearchParams({ level: show, limit: "200" });
  if (search.trim()) params.set("q", search.trim());

  const logs = useQuery({
    queryKey: ["runner-logs", runnerId, show, search.trim()],
    queryFn: () => api<LogLine[]>(`/runners/${runnerId}/logs?${params}`),
    refetchInterval: 5_000,
  });

  const setLevel = useMutation({
    mutationFn: (next: string) =>
      post<{ level: string; applied: boolean; error?: string }>(`/runners/${runnerId}/log-level`, { level: next }),
    onSuccess: (res) => {
      // Gespeichert, aber nicht zugestellt, ist ein eigener Zustand: Ein
      // Host, der gerade weg ist, uebernimmt es bei der naechsten Verbindung.
      // Das zu verschweigen liest sich wie „erledigt".
      setNote(res.applied ? "" : t("runners.log.levelPending", { error: res.error ?? "" }));
      qc.invalidateQueries({ queryKey: ["runners"] });
    },
    onError: (e) => setNote(String((e as Error)?.message)),
  });

  const time = (iso: string) => new Date(iso).toLocaleString(locale, { hour12: false });
  const cls = (l: string) => (l === "error" ? "danger-text" : l === "warn" ? "warn-text" : "muted");

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

      {logs.isLoading && <p className="muted text-sm">{t("common.loading")}</p>}
      {logs.data?.length === 0 && ships && <p className="muted text-sm">{t("runners.log.empty")}</p>}

      {(logs.data ?? []).length > 0 && (
        <div style={{ maxHeight: 420, overflowY: "auto", overflowX: "auto" }}>
          {(logs.data ?? []).map((l) => (
            <div key={l.id} className="mono text-[12px] flex gap-2" style={{ padding: "2px 0", whiteSpace: "nowrap" }}>
              <span className="muted">{time(l.ts)}</span>
              <span className={cls(l.level)} style={{ width: 44, display: "inline-block" }}>
                {l.level}
              </span>
              <span>{l.msg}</span>
              {l.attrs &&
                Object.entries(l.attrs).map(([k, v]) => (
                  <span key={k} className="muted">
                    {k}={v}
                  </span>
                ))}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
