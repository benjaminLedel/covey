import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import i18n from "../../i18n";
import {
  api,
  type RecordingEvent,
} from "../../api";
import { ActivityFeed, subAgentMark } from "../../components/ActivityFeed";

export function Recording({
  agentId,
  taskFilter,
  onClearFilter,
}: {
  agentId: string;
  taskFilter?: { id: string; title: string } | null;
  onClearFilter?: () => void;
}) {
  const { t } = useTranslation();
  const [view, setView] = useState<"feed" | "raw">("feed");
  const events = useQuery({
    queryKey: ["recording", agentId, taskFilter?.id ?? null],
    queryFn: () =>
      api<RecordingEvent[] | null>(
        `/agents/${agentId}/recording${taskFilter ? `?task_id=${taskFilter.id}` : ""}`,
      ),
    refetchInterval: 5000,
  });
  const list = events.data ?? [];
  // Wo dieser Agent arbeitet. Die Auskunft steckte bisher als eine graue Zeile
  // zwischen Hunderten im Verlauf — dabei ist sie ab der zweiten Maschine die
  // erste Frage vor einem Lauf, der sich seltsam verhalten hat, und die einzige
  // Antwort, die man nicht aus dem Rest herauslesen kann.
  const host = useMemo(() => placedOn(list), [list]);
  return (
    <div>
      <div className="flex items-center gap-2 mb-3">
        {host && (
          <span className="muted text-sm">
            {host.live ? t("agent.recording.runsOn") : t("agent.recording.ranOn")}{" "}
            <Link to={`/infrastructure/runners/${host.id}`}>{host.name}</Link>
          </span>
        )}
        {taskFilter && (
          <>
            <span className="badge st-triage">{t("agent.recording.taskFilter", { title: taskFilter.title })}</span>
            <button className="btn sm" onClick={onClearFilter}>
              {t("agent.recording.allEvents")}
            </button>
          </>
        )}
        <span className="flex-1" />
        <div className="seg" role="tablist" aria-label="Ansicht">
          <button
            className={view === "feed" ? "active" : ""}
            onClick={() => setView("feed")}
            title={t("agent.recording.readableTitle")}
          >
            {t("agent.recording.readableView")}
          </button>
          <button
            className={view === "raw" ? "active" : ""}
            onClick={() => setView("raw")}
            title={t("agent.recording.rawTitle")}
          >
            {t("agent.recording.rawView")}
          </button>
        </div>
      </div>
      <div className="card">
        {/* Solange die Abfrage laeuft, ist die Liste leer — und „noch keine
            Aufzeichnung" ist dann eine Falschaussage, die aussieht wie ein
            Befund. Bei einem Agenten mit 178 Laeufen liest man sie als Fehler
            und sucht an der falschen Stelle. */}
        {events.isLoading && <p className="muted m-0">{t("common.loading")}</p>}
        {!events.isLoading && list.length === 0 && (
          <p className="muted m-0">
            {taskFilter ? t("agent.recording.emptyFiltered") : t("agent.recording.emptyAll")}
          </p>
        )}
        {list.length > 0 &&
          (view === "feed" ? (
            <ActivityFeed events={list} truncated={list.length >= 500} />
          ) : (
            [...list].reverse().map((e) => <RecordingItem key={e.id} event={e} />)
          ))}
      </div>
    </div>
  );
}

// placedOn liest aus dem Verlauf, auf welchem Host der Agent zuletzt gelandet
// ist — und ob er dort noch ist. Beides steht in den Lebenszyklus-Ereignissen,
// die ohnehin geladen sind: kein zweiter Aufruf fuer eine Zeile.
//
// „Noch dort" ist absichtlich vorsichtig: Was den Lauf beendet, ist ein
// spaeteres schlafen/wake_failed. Fehlt das, laeuft er.
function placedOn(list: RecordingEvent[]): { id: string; name: string; live: boolean } | null {
  let host: { id: string; name: string; live: boolean } | null = null;
  for (const e of list) {
    if (e.kind !== "lifecycle") continue;
    const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<string, any>;
    if (p.status === "sandbox" && p.runner) {
      host = { id: String(p.runner), name: String(p.runner_name || p.runner).slice(0, 40), live: true };
    } else if (host && (p.status === "sleeping" || p.status === "wake_failed" || p.status === "killed")) {
      host.live = false;
    }
  }
  return host;
}

function summarize(e: RecordingEvent): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<string, any>;
  switch (e.kind) {
    case "lifecycle": {
      if (p.status === "task_done") return { text: i18n.t("activity.taskDone") };
      // Wo ein Lauf stattfindet. Bei einer Maschine ist das keine Frage, ab
      // der zweiten ist es die erste — und die Antwort stand vorher nur im Log
      // des Prozesses, wenn überhaupt.
      if (p.status === "sandbox")
        return {
          text: i18n.t("activity.sandboxOn", { runner: (p.runner_name as string) || (p.runner as string) }),
          muted: true,
        };
      const label = i18n.t(`status.${p.status as string}`, p.status as string);
      return { text: i18n.t("activity.statusChange", { label }), muted: p.status === "sleeping" };
    }
    case "credential": {
      const ttl = p.ttl_secs ? i18n.t("activity.credentialTtl", { min: Math.round(p.ttl_secs / 60) }) : "";
      if (p.granted) return {
        text: i18n.t("activity.credentialGranted", {
          system: p.system,
          proactive: p.proactive ? i18n.t("activity.credentialProactive") : "",
          ttl,
        })
      };
      return {
        text: i18n.t("activity.credentialDenied", {
          system: p.system,
          reason: p.reason ? i18n.t("activity.credentialDeniedReason", { reason: p.reason }) : "",
        }),
        danger: true,
      };
    }
    case "approval": {
      const decision =
        p.decision === "auto-allow"
          ? i18n.t("activity.approvalAuto")
          : i18n.t(`status.${p.decision as string}`, p.decision as string);
      return { text: `${p.action} — ${decision}`, danger: p.decision === "denied" };
    }
    case "guardrail":
      return {
        text: i18n.t("activity.guardrailTriggered", {
          rule: p.rule ?? "",
          what: (p.action ?? p.system) ? i18n.t("activity.guardrailWhat", { what: p.action ?? p.system ?? "" }) : "",
          pattern: p.pattern ? i18n.t("activity.guardrailPattern", { pattern: p.pattern }) : "",
        }),
        danger: true,
      };
    case "action":
      return { text: i18n.t("activity.targetAction", { action: p.action ?? "?" }), mono: true };
    case "runtime":
      return summarizeRuntime(p);
  }
  return { text: JSON.stringify(e.payload), mono: true };
}

function summarizeRuntime(p: Record<string, any>): { text: string; mono?: boolean; muted?: boolean; danger?: boolean } {
  if (typeof p.text === "string" && !p.message) return { text: p.text };
  switch (p.type) {
    case "system":
      return {
        text: i18n.t("activity.sessionStarted", {
          model: p.model ? i18n.t("activity.withModel", { model: p.model }) : "",
        }),
        muted: true,
      };
    case "rate_limit_event":
      return { text: i18n.t("activity.rateLimitUpdated"), muted: true };
    case "assistant": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const parts: string[] = [];
      for (const b of blocks) {
        if (b.type === "text" && b.text) parts.push(truncate(b.text, 400));
        if (b.type === "tool_use") parts.push(`⚙ ${b.name}(${truncate(compactInput(b.input), 120)})`);
      }
      return parts.length ? { text: parts.join("  ·  ") } : { text: i18n.t("activity.runtimeResponse"), muted: true };
    }
    case "user": {
      const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
      const res = blocks.find((b) => b.type === "tool_result");
      if (res) {
        const body = typeof res.content === "string" ? res.content : JSON.stringify(res.content);
        return { text: i18n.t("activity.toolResultText", { text: truncate(body, 200) }), mono: true, muted: true };
      }
      return { text: i18n.t("activity.runtimeInput"), muted: true };
    }
    case "result": {
      const cost = p.total_cost_usd ? ` · $${Number(p.total_cost_usd).toFixed(4)}` : "";
      const tok = p.usage?.input_tokens ? ` · ${p.usage.input_tokens}→${p.usage.output_tokens} Tokens` : "";
      if (p.is_error) return { text: i18n.t("activity.runFailedText", { text: truncate(p.result ?? "", 300) }), danger: true };
      return { text: i18n.t("activity.runResultText", { text: truncate(p.result ?? "", 400), cost, tokens: tok }) };
    }
  }
  return { text: JSON.stringify(p), mono: true };
}

const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n) + "…" : s);

const compactInput = (input: unknown) => {
  if (!input || typeof input !== "object") return "";
  return Object.entries(input as Record<string, unknown>)
    .map(([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`)
    .join(", ");
};

function RecordingItem({ event }: { event: RecordingEvent }) {
  const s = summarize(event);
  const raw = typeof event.payload === "string" ? event.payload : JSON.stringify(event.payload, null, 2);
  const locale = i18n.language === "de" ? "de-DE" : "en-US";
  // Rohansicht und erzählende Ansicht müssen dasselbe sagen: Gehört die Zeile
  // zu einem Sub-Lauf, steht das hier als Badge — dort als eigener Block.
  const sub = subAgentMark(event);
  return (
    <div className="timeline-item">
      <span className={`kind-tag kind-${event.kind}`}>{event.kind}</span>
      {sub && <span className="kind-tag kind-subagent">{i18n.t("activity.subAgentBadge")}</span>}
      <details className="flex-1 min-w-0 rec-details">
        <summary
          className={`rec-summary break-words ${s.mono ? "mono" : ""}`}
          style={{
            color: s.danger ? "var(--text-warning, #b45309)" : s.muted ? "var(--text-secondary)" : undefined,
            whiteSpace: "pre-wrap",
          }}
        >
          {s.text}
        </summary>
        <pre className="rec-raw">{raw}</pre>
      </details>
      <span className="muted shrink-0 text-[11px]">
        {new Date(event.created_at).toLocaleTimeString(locale)}
      </span>
    </div>
  );
}
