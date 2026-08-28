import { useTranslation } from "react-i18next";
import i18n from "../i18n";
import { type RecordingEvent, recordingBlobURL } from "../api";
import { fmtBytes, fmtCount, fmtDelta, fmtUSD } from "../format";

// ActivityFeed: übersetzt das lückenlose Recording in eine erzählende
// Aktivitätsansicht im Stil des Mockups — Turns mit der Stimme des Agenten
// (Lora), aufklappbare Tool-Aufrufe mit Status-Pill, Ereignis- und
// Gate-Zeilen für Lifecycle/Credential/Approval/Guardrail sowie ein
// Parked-Block, wenn der Agent auf ein externes Ereignis wartet.
// Tool-Aufruf und Tool-Ergebnis werden über die tool_use_id korreliert,
// sodass Aufruf und Antwort als ein Eintrag erscheinen.
//
// Sub-Läufe (der Sub-Agent im Projekt-Checkout, spec/12) tragen ihre
// Markierung IM Payload und landen in derselben Aufzeichnung wie der äußere
// Lauf. Der Feed fasst sie zu einem eigenen, zugeklappten Block zusammen —
// sonst stünde die Arbeit des Sub-Agenten ununterscheidbar zwischen der des
// äußeren Agenten, und man wüsste nicht mehr, wer was getan hat.

type Tone = "ok" | "warn" | "danger" | "muted";

type ToolCall = {
  key: string;
  name: string;
  detail: string; // kompakte Argument-Vorschau im Summary
  input?: unknown;
  result?: string;
  imageURL?: string; // Screenshot-Artefakt (browser), inline gerendert
  isError?: boolean;
  pending: boolean;
  // Laufzeit eines noch offenen Aufrufs, aus dem Herzschlag der Runtime
  // (tool_progress). Ohne sie sagt eine offene Zeile nur „läuft" — und ein
  // zehn Minuten alter Bash-Aufruf sieht aus wie ein Hänger.
  elapsedSeconds?: number;
};

type GateData = { icon: IconName; text: string; time: string; tone: Tone };

type TurnRow = { type: "tool"; call: ToolCall } | { type: "gate"; gate: GateData };

type FeedItem = { key: string } & (
  | { kind: "day"; text: string }
  | { kind: "evt"; icon: IconName; text: string; time: string; tone?: Tone; count?: number }
  | { kind: "turn"; time: string; voice: string[]; rows: TurnRow[] }
  | { kind: "gate"; icon: IconName; text: string; time: string; tone: Tone }
  | { kind: "parked"; title: string; text: string; time: string }
  | { kind: "result"; ok: boolean; text: string; meta: string; time: string }
  | {
      // Eine Phase der Plattform selbst: ein Image wird geholt, ein Home
      // hergestellt oder zurückgeschrieben. Anders als ein Ereignis ist das
      // eine Zeile, die sich ändert — solange die Phase läuft, tragen die
      // folgenden Meldungen ihre Zahlen in DIESE Zeile nach, statt sich
      // darunter zu stapeln.
      kind: "phase";
      phase: string;
      detail?: string;
      time: string;
      done: boolean;
      bytes?: number;
      bytesTotal?: number;
      count?: number;
      countTotal?: number;
      ms?: number;
      // Der Vorgang ist nicht durchgelaufen. Eine Phase mit Fehler ist kein
      // Fortschritt mehr, sondern ein Befund — und der Balken hat dort nichts
      // zu suchen.
      error?: string;
    }
  | {
      kind: "subagent";
      dir: string;
      task?: string;
      time: string;
      meta: string;
      state: "running" | "ok" | "failed";
      items: FeedItem[];
    }
);

// SubAgentMark ist die Markierung, die der Daemon jeder stream-json-Zeile eines
// Sub-Laufs mitgibt (markSubAgent in internal/daemon/subagent.go). dir und run
// stehen auf jeder Zeile, task nur auf der ERSTEN.
export type SubAgentMark = { dir: string; run?: string; task?: string };

// subAgentMark liest die Markierung aus einem Recording-Event. null = das
// Event gehört zum äußeren Lauf.
export function subAgentMark(e: RecordingEvent): SubAgentMark | null {
  if (!e.payload || typeof e.payload !== "object") return null;
  const m = (e.payload as Record<string, unknown>).covey_sub_agent;
  if (!m || typeof m !== "object") return null;
  const { dir, run, task } = m as Record<string, unknown>;
  return {
    dir: typeof dir === "string" ? dir : "",
    run: typeof run === "string" && run ? run : undefined,
    task: typeof task === "string" && task ? task : undefined,
  };
}

// runKey identifiziert den Lauf, zu dem eine Zeile gehört. Die Kennung des
// Daemons ist die verlässliche Quelle; für Aufzeichnungen, die noch vor ihrer
// Einführung entstanden sind, muss das Verzeichnis herhalten.
const runKey = (m: SubAgentMark) => m.run ?? `dir:${m.dir}`;

// shortDir kürzt den Checkout-Pfad auf die letzten beiden Segmente:
// /home/agent/repos/covey-main-abc123 → repos/covey-main-abc123. Der volle
// Pfad ist als Überschrift zu lang und sagt nicht mehr aus.
function shortDir(dir: string): string {
  const parts = dir.split("/").filter(Boolean);
  return parts.slice(-2).join("/") || dir;
}

// --- Icons: kleine Inline-SVGs im Strichstil der übrigen UI ---

type IconName =
  | "bolt"
  | "moon"
  | "check"
  | "shield"
  | "key"
  | "clock"
  | "x"
  | "flag"
  | "info"
  | "layers"
  | "download"
  | "save"
  | "box"
  | "file";

const paths: Record<IconName, string> = {
  // download: etwas kommt auf diesen Host — ein Image, ein Arbeitsplatz.
  download: "M12 3v12m0 0l-4-4m4 4l4-4M4 19h16",
  // save: etwas geht von diesem Host weg, in den Speicher.
  save: "M12 21V9m0 0l-4 4m4-4l4 4M4 5h16",
  // file: eine Änderung am Arbeitsplatz — von Menschenhand, nicht vom Agenten.
  file: "M6 3h7l5 5v13H6V3zm7 0v5h5",
  // box: ein Dienst neben der Sandbox — ein Container, den der Agent nicht
  // betreibt und der mit der Sandbox wieder verschwindet.
  box: "M12 3l8 4.5v9L12 21l-8-4.5v-9L12 3zm0 0v18m8-13.5L4 16.5",
  // layers: der Sub-Lauf — eine zweite Ebene unter der laufenden Arbeit.
  layers: "M12 3l8 4.5-8 4.5-8-4.5L12 3zm-8 9l8 4.5 8-4.5",
  bolt: "M13 2L4 14h6l-1 8 9-12h-6l1-8z",
  moon: "M21 12.8A9 9 0 1 1 11.2 3 7 7 0 0 0 21 12.8z",
  check: "M4 12.5l5 5L20 6.5",
  shield: "M12 3l7 3v5c0 4.5-3 8.5-7 10-4-1.5-7-5.5-7-10V6l7-3z",
  key: "M21 2l-9.6 9.6M15.5 7.5l3 3M11 13a4 4 0 1 1-6 6 4 4 0 0 1 6-6z",
  clock: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zm0 4v5l3.5 2",
  x: "M6 6l12 12M18 6L6 18",
  flag: "M5 21V4m0 1h12l-2.5 3.5L17 12H5",
  info: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zm0 5v.5m0 3v5",
};

function Icon({ name }: { name: IconName }) {
  return (
    <svg viewBox="0 0 24 24" className="act-ic" aria-hidden="true">
      <path d={paths[name]} />
    </svg>
  );
}

// --- Feed-Aufbau aus den chronologischen Recording-Events ---

const fmtTime = (iso: string) =>
  new Date(iso).toLocaleTimeString(i18n.language === "de" ? "de-DE" : "en-US", { hour: "2-digit", minute: "2-digit" });

const fmtDay = (iso: string) =>
  new Date(iso).toLocaleDateString(i18n.language === "de" ? "de-DE" : "en-US", { day: "numeric", month: "long", year: "numeric" });

const truncate = (s: string, n: number) => (s.length > n ? s.slice(0, n) + " …" : s);

// prettyToolName: "mcp__zammad__ticket_search" → "zammad · ticket_search".
function prettyToolName(name: string): string {
  if (name.startsWith("mcp__")) {
    const parts = name.split("__");
    return parts.slice(1).join(" · ");
  }
  return name;
}

// toolDetail: die eine Zeile Argument-Vorschau im Tool-Summary.
function toolDetail(name: string, input: unknown): string {
  if (!input || typeof input !== "object") return "";
  const o = input as Record<string, unknown>;
  if (name === "Bash" && typeof o.command === "string") return o.command;
  if (typeof o.file_path === "string") return o.file_path;
  if (typeof o.pattern === "string") return o.pattern;
  if (typeof o.url === "string") return o.url;
  const parts = Object.entries(o).map(
    ([k, v]) => `${k}: ${typeof v === "string" ? v : JSON.stringify(v)}`,
  );
  return parts.join(" · ");
}

// resultText: Tool-Ergebnisse kommen als String oder als Content-Block-Liste.
function resultText(content: unknown): string {
  if (typeof content === "string") return content;
  if (Array.isArray(content)) {
    return content
      .map((b) => (b && typeof b === "object" && "text" in b ? String((b as any).text) : ""))
      .filter(Boolean)
      .join("\n");
  }
  return content == null ? "" : JSON.stringify(content, null, 2);
}

type TurnItem = Extract<FeedItem, { kind: "turn" }>;

export function buildFeed(events: RecordingEvent[]): FeedItem[] {
  return buildItems(events, false);
}

// buildItems ist der eigentliche Aufbau. nested=true heißt: Wir bauen den
// Inhalt EINES Sub-Laufs. Dann entfallen Datumstrenner (im Block will niemand
// einen) und die system-Zeile der Runtime („Sitzung gestartet") — dass hier
// eine eigene Session läuft, sagt der Kopf des Blocks bereits.
function buildItems(events: RecordingEvent[], nested: boolean): FeedItem[] {
  const items: FeedItem[] = [];
  const toolIndex = new Map<string, ToolCall>();
  let turn: TurnItem | null = null;
  let lastDay = "";

  const closeTurn = () => {
    turn = null;
  };
  const ensureTurn = (time: string, key: string): TurnItem => {
    if (!turn) {
      turn = { key, kind: "turn", time, voice: [], rows: [] };
      items.push(turn);
    }
    return turn;
  };
  const pushGate = (gate: GateData, key: string) => {
    if (turn) turn.rows.push({ type: "gate", gate });
    else items.push({ key, kind: "gate", ...gate });
  };
  // Die offene Phase je Art: solange sie läuft, trägt jede weitere Meldung
  // ihre Zahlen in dieselbe Zeile nach. Fünfzehn Minuten Sichern sind EIN
  // Vorgang und keine sechzig Zeilen.
  const offenePhasen = new Map<string, Extract<FeedItem, { kind: "phase" }>>();
  const pushPhase = (p: Record<string, unknown>, time: string, key: string) => {
    const phase = String(p.phase ?? "");
    if (!phase) return;
    const zahl = (v: unknown) => (typeof v === "number" && v > 0 ? v : undefined);
    const done = p.done === true || phase === "home_synced";
    const offen = offenePhasen.get(phase);
    const ziel: Extract<FeedItem, { kind: "phase" }> = offen ?? {
      key,
      kind: "phase",
      phase,
      time,
      done: false,
    };
    ziel.time = time;
    ziel.done = done;
    if (typeof p.detail === "string" && p.detail) ziel.detail = p.detail;
    if (typeof p.error === "string" && p.error) ziel.error = p.error;
    // Zahlen nur übernehmen, wenn welche da sind: die Anfangsmeldung hat
    // keine, und sie darf die letzte Zwischenmeldung nicht auf null setzen.
    ziel.bytes = zahl(p.bytes) ?? ziel.bytes;
    ziel.bytesTotal = zahl(p.bytes_total) ?? ziel.bytesTotal;
    ziel.count = zahl(p.count) ?? ziel.count;
    ziel.countTotal = zahl(p.count_total) ?? ziel.countTotal;
    ziel.ms = zahl(p.ms) ?? ziel.ms;
    if (!offen) items.push(ziel);
    if (done) offenePhasen.delete(phase);
    else offenePhasen.set(phase, ziel);
  };
  const pushEvt = (evt: Extract<FeedItem, { kind: "evt" }>) => {
    const last = items[items.length - 1];
    if (last?.kind === "evt" && last.text === evt.text && last.tone === evt.tone) {
      last.count = (last.count ?? 1) + 1;
      last.time = evt.time;
      return;
    }
    items.push(evt);
  };

  // Sub-Läufe vorab nach ihrer Kennung bündeln, statt sie an ihrer
  // Nachbarschaft im Strom zu erkennen. Der Unterschied zählt: Der
  // Action-Proxy bedient nebenläufig, zwei gleichzeitige `dev agent`-Aufrufe
  // verschränken also ihre Zeilen. Über die Kennung bleibt trotzdem jeder Lauf
  // ein Block; über Nachbarschaft verschmölzen sie oder zerfielen in Stücke.
  const runs = new Map<string, RecordingEvent[]>();
  if (!nested) {
    for (const e of events) {
      const m = subAgentMark(e);
      if (!m) continue;
      const bucket = runs.get(runKey(m));
      if (bucket) bucket.push(e);
      else runs.set(runKey(m), [e]);
    }
  }
  const emitted = new Set<string>();

  for (const e of events) {
    const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<
      string,
      any
    >;
    const time = fmtTime(e.created_at);
    const day = fmtDay(e.created_at);
    const k = `e${e.id}`;
    if (!nested && day !== lastDay) {
      lastDay = day;
      closeTurn();
      items.push({ key: `day-${day}`, kind: "day", text: day });
    }

    // Der Block steht dort, wo der Lauf beginnt; seine weiteren Zeilen sind
    // damit abgehandelt.
    const mark = nested ? null : subAgentMark(e);
    if (mark) {
      const key = runKey(mark);
      if (!emitted.has(key)) {
        emitted.add(key);
        closeTurn();
        items.push(subAgentItem(runs.get(key) ?? [e], mark));
      }
      continue;
    }

    switch (e.kind) {
      case "runtime": {
        // Im Block ist die eigene Session des Sub-Laufs kein Ereignis.
        if (nested && p.type === "system") break;
        if (typeof p.text === "string" && !p.message && !p.type) {
          ensureTurn(time, k).voice.push(p.text);
          break;
        }
        switch (p.type) {
          // Ein `system`-Ereignis ist nicht gleich ein Sitzungsstart. Die
          // Runtime schickt unter demselben Typ auch ihren Token-Zähler und
          // den Zustand ihrer Hintergrundaufgaben — in einem gemessenen Lauf
          // waren von 192 system-Zeilen ganze ZWEI ein `init`. Ohne diese
          // Unterscheidung behauptet der Verlauf 192 Sitzungsstarts, und wer
          // ihn als Beleg liest, liest etwas Falsches.
          case "system": {
            const subtype = String(p.subtype ?? "");
            // thinking_tokens ist ein Zähler, task_updated und
            // background_tasks_changed sind interne Zustandsspiegel: Sie
            // gehören nicht in einen Lesetext. Was sie aussagen, steht
            // ohnehin in den Zeilen daneben.
            if (
              subtype === "thinking_tokens" ||
              subtype === "task_updated" ||
              subtype === "background_tasks_changed"
            ) {
              break;
            }
            // Eine Hintergrundaufgabe ist echte Arbeit (ein `Bash`-Lauf, der
            // den Turn überdauert) und wird deshalb benannt, nicht verworfen.
            if (subtype === "task_started" || subtype === "task_notification") {
              closeTurn();
              const what = String(p.description ?? p.summary ?? "");
              pushEvt({
                key: k,
                kind: "evt",
                icon: "clock",
                text:
                  subtype === "task_started"
                    ? i18n.t("activity.backgroundTaskStarted", { what })
                    : i18n.t("activity.backgroundTaskEnded", { what }),
                time,
                tone: "muted",
              });
              break;
            }
            // `init` — und Aufzeichnungen aus der Zeit vor den Subtypen, die
            // gar keinen tragen.
            if (subtype !== "" && subtype !== "init") break;
            closeTurn();
            pushEvt({
              key: k,
              kind: "evt",
              icon: "info",
              text: i18n.t("activity.sessionStarted", {
                model: p.model ? i18n.t("activity.withModel", { model: p.model }) : "",
              }),
              time,
              tone: "muted",
            });
            break;
          }
          // Der Herzschlag eines laufenden Werkzeugs: alle 30 Sekunden eine
          // Zeile, damit ein langer Aufruf von einem Hänger unterscheidbar
          // bleibt. Er ist kein Ereignis für sich — er gehört an den offenen
          // Aufruf, den er beschreibt.
          case "tool_progress": {
            const parent = p.parent_tool_use_id ? String(p.parent_tool_use_id) : "";
            const call = parent ? toolIndex.get(parent) : undefined;
            if (call?.pending && typeof p.elapsed_time_seconds === "number") {
              call.elapsedSeconds = p.elapsed_time_seconds;
            }
            break;
          }
          case "rate_limit_event":
            break;
          case "assistant": {
            const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
            for (const b of blocks) {
              if (b.type === "text" && b.text) {
                const cur = turn as TurnItem | null;
                if (cur && (cur.voice.length > 0 || cur.rows.length > 0)) closeTurn();
                ensureTurn(time, k).voice.push(b.text);
              }
              if (b.type === "tool_use") {
                const call: ToolCall = {
                  key: b.id ?? `${e.id}-${(turn as TurnItem | null)?.rows.length ?? 0}`,
                  name: prettyToolName(b.name ?? "Tool"),
                  detail: toolDetail(b.name ?? "", b.input),
                  input: b.input,
                  pending: true,
                };
                ensureTurn(time, k).rows.push({ type: "tool", call });
                if (b.id) toolIndex.set(b.id, call);
              }
            }
            break;
          }
          case "user": {
            const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
            for (const b of blocks) {
              if (b.type !== "tool_result") continue;
              const call = b.tool_use_id ? toolIndex.get(b.tool_use_id) : undefined;
              if (call) {
                call.result = resultText(b.content);
                call.isError = Boolean(b.is_error);
                call.pending = false;
              }
            }
            break;
          }
          case "result": {
            closeTurn();
            const meta = [
              p.total_cost_usd ? fmtUSD(Number(p.total_cost_usd)) : "",
              p.usage?.input_tokens
                ? `${p.usage.input_tokens} → ${p.usage.output_tokens} Tokens`
                : "",
              p.num_turns ? `${p.num_turns} Turns` : "",
            ]
              .filter(Boolean)
              .join(" · ");
            items.push({
              key: k,
              kind: "result",
              ok: !p.is_error,
              text: String(p.result ?? (p.is_error ? i18n.t("activity.runFailed") : i18n.t("activity.runEnded"))),
              meta,
              time,
            });
            break;
          }
          // Was die Runtime sonst noch schickt, wird benannt und nicht
          // ausgeschüttet: eine rohe JSON-Zeile im Verlauf ist der Grund,
          // warum niemand mehr hinsieht. Der Typ reicht, um zu erkennen, dass
          // hier etwas Unbehandeltes ankommt — und gleiche Zeilen fasst
          // pushEvt zu einer mit Zähler zusammen.
          default:
            closeTurn();
            pushEvt({
              key: k,
              kind: "evt",
              icon: "info",
              text: i18n.t("activity.runtimeEvent", { type: String(p.type ?? "?") }),
              time,
              tone: "muted",
            });
        }
        break;
      }

      case "lifecycle": {
        closeTurn();
        const status = String(p.status ?? "");
        if (status === "preparing") {
          pushPhase(p, time, k);
        } else if (status === "blocked") {
          items.push({
            key: k,
            kind: "parked",
            title: i18n.t("activity.waitingForEvent"),
            text: i18n.t("activity.parkedSince", {
              question: p.question ? i18n.t("activity.parkedQuestion", { question: p.question }) : "",
              correlation: p.correlation_key
                ? i18n.t("activity.parkedCorrelation", { key: p.correlation_key })
                : "",
              time,
            }),
            time,
          });
        } else if (status === "wake_on_correlation") {
          items.push({
            key: k,
            kind: "evt",
            icon: "bolt",
            text: i18n.t("activity.wokeOnEvent", {
              key: p.correlation_key ? i18n.t("activity.wokeOnEventKey", { key: p.correlation_key }) : "",
            }),
            time,
          });
        } else if (status === "working" || status === "triggered" || status === "triage") {
          items.push({ key: k, kind: "evt", icon: "bolt", text: i18n.t("activity.woke"), time });
        } else if (status === "sleeping") {
          items.push({
            key: k,
            kind: "evt",
            icon: "moon",
            text: i18n.t("activity.sleeping"),
            time,
            tone: "muted",
          });
        } else if (status === "task_done") {
          items.push({ key: k, kind: "gate", icon: "check", text: i18n.t("activity.taskDone"), time, tone: "ok" });
        } else if (status === "task_failed") {
          items.push({ key: k, kind: "gate", icon: "x", text: i18n.t("activity.taskFailed"), time, tone: "danger" });
        } else if (status === "task_blocked") {
          items.push({
            key: k,
            kind: "gate",
            icon: "clock",
            text: i18n.t("activity.taskBlocked"),
            time,
            tone: "warn",
          });
        } else if (status === "stage") {
          items.push({ key: k, kind: "evt", icon: "flag", text: i18n.t("activity.stage", { stage: p.stage }), time });
        } else if (status === "stale") {
          // Die Plattform hat einen Zustand aufgelöst, hinter dem nichts mehr
          // stand. Ohne diese Zeile schliefe der Agent „einfach so", und die
          // Stunde davor bliebe unerklärt.
          items.push({
            key: k,
            kind: "gate",
            icon: "clock",
            text: i18n.t("activity.stale", { was: i18n.t(`status.${p.was}`, String(p.was ?? "")) }),
            time,
            tone: "warn",
          });
        } else if (status === "killed") {
          items.push({ key: k, kind: "gate", icon: "x", text: i18n.t("activity.killed"), time, tone: "danger" });
        } else {
          items.push({
            key: k,
            kind: "evt",
            icon: "info",
            text: i18n.t("activity.statusChange", { label: i18n.t(`status.${status}`, status) }),
            time,
          });
        }
        break;
      }

      case "credential": {
        if (p.granted) {
          pushGate({
            icon: "key",
            text: i18n.t("activity.credentialGranted", {
              system: p.system,
              proactive: p.proactive ? i18n.t("activity.credentialProactive") : "",
              ttl: p.ttl_secs ? i18n.t("activity.credentialTtl", { min: Math.round(p.ttl_secs / 60) }) : "",
            }),
            time,
            tone: "ok",
          }, k);
        } else {
          pushGate({
            icon: "key",
            text: i18n.t("activity.credentialDenied", {
              system: p.system,
              reason: p.reason ? i18n.t("activity.credentialDeniedReason", { reason: p.reason }) : "",
            }),
            time,
            tone: "danger",
          }, k);
        }
        break;
      }

      case "approval": {
        const d = String(p.decision ?? "");
        if (d === "pending") {
          pushGate({
            icon: "clock",
            text: i18n.t("activity.approvalPending", { action: p.action }),
            time,
            tone: "warn",
          }, k);
        } else if (d === "denied") {
          pushGate({ icon: "x", text: i18n.t("activity.approvalDenied", { action: p.action }), time, tone: "danger" }, k);
        } else {
          const how = d === "auto-allow" ? i18n.t("activity.approvalAuto") : i18n.t("activity.approvalManual");
          pushGate({ icon: "shield", text: i18n.t("activity.approvalGranted", { how, action: p.action }), time, tone: "ok" }, k);
        }
        break;
      }

      case "guardrail": {
        const what = p.action ?? p.system ?? "";
        pushGate({
          icon: "shield",
          text: i18n.t("activity.guardrailTriggered", {
            rule: p.rule ?? "",
            what: what ? i18n.t("activity.guardrailWhat", { what }) : "",
            pattern: p.pattern ? i18n.t("activity.guardrailPattern", { pattern: p.pattern }) : "",
          }),
          time,
          tone: "danger",
        }, k);
        break;
      }

      /* Was neben der Sandbox lief (spec/16). Drei Zustände, und sie
         beantworten drei verschiedene Fragen: `started` sagt, was beim Wecken
         hochgefahren wurde, `running` steht auf JEDEM Job und sagt, wogegen
         genau dieser Lauf gearbeitet hat (bei einer warmen Sandbox kamen die
         Dienste womöglich Stunden vorher hoch), und `refused` sagt, warum es
         keinen Lauf gab.

         Das Image-Id steht bewusst dabei: Ein Tag ist ein bewegliches Ziel,
         und die Frage nach einem halben Jahr lautet nicht, welcher Tag
         konfiguriert war, sondern welche Bytes liefen. */
      case "service": {
        const list = Array.isArray(p.services) ? (p.services as Record<string, unknown>[]) : [];
        const names = list
          .map((svc) =>
            svc.image_id
              ? `${svc.name} = ${svc.image} (${String(svc.image_id).slice(0, 19)})`
              : `${svc.name} = ${svc.image}`,
          )
          .join(", ");
        if (p.status === "refused") {
          pushGate({
            icon: "shield",
            text: i18n.t("activity.servicesRefused", { services: names, error: p.error ?? "" }),
            time,
            tone: "danger",
          }, k);
          break;
        }
        closeTurn();
        items.push({
          key: k,
          kind: "evt",
          icon: "box",
          text: i18n.t(p.status === "running" ? "activity.servicesRunning" : "activity.servicesStarted", {
            services: names,
            count: list.length,
          }),
          time,
          tone: "muted",
        });
        break;
      }

      // Ein Mensch hat am Arbeitsplatz des Agenten etwas geändert. Steht
      // bewusst in derselben Spur wie die Aktionen des Agenten: für den, der
      // den Lauf später liest, ist eine über Nacht ausgetauschte Datei
      // dieselbe Art von Ereignis wie ein Tool-Aufruf.
      case "file": {
        closeTurn();
        const ops: Record<string, string> = {
          write: "activity.fileWrite",
          upload: "activity.fileUpload",
          mkdir: "activity.fileMkdir",
          move: "activity.fileMove",
          delete: "activity.fileDelete",
        };
        items.push({
          key: k,
          kind: "evt",
          icon: "file",
          text: i18n.t(ops[String(p.op)] ?? "activity.fileWrite", {
            path: p.path,
            actor: p.actor || i18n.t("activity.fileSomebody"),
          }),
          time,
        });
        break;
      }

      case "action": {
        const call: ToolCall = {
          key: `action-${e.id}`,
          name: i18n.t("activity.targetAction", { action: p.action ?? "" }),
          detail: toolDetail("", p.params ?? p),
          input: p,
          pending: false,
          isError: p.ok === false,
          result: p.result != null ? resultText(p.result) : undefined,
          imageURL: typeof p.screenshot === "string" ? recordingBlobURL(p.screenshot) : undefined,
        };
        ensureTurn(time, k).rows.push({ type: "tool", call });
        break;
      }

      default:
        closeTurn();
        items.push({
          key: k,
          kind: "evt",
          icon: "info",
          text: truncate(JSON.stringify(e.payload), 160),
          time,
          tone: "muted",
        });
    }
  }
  return items;
}

// subAgentItem baut aus den Events eines Sub-Laufs den zugeklappten Block.
// Der Inhalt entsteht durch denselben Aufbau noch einmal — der Sub-Lauf
// bekommt dadurch sein eigenes tool_use/tool_result-Register und korreliert
// sauber in sich, statt sich mit dem äußeren Lauf zu vermischen.
function subAgentItem(group: RecordingEvent[], mark: SubAgentMark): FeedItem {
  const inner = buildItems(group, true);
  const first = group[0];
  const last = group[group.length - 1];

  // Kennzahlen aus den result-Zeilen selbst, nicht aus dem fertigen Item:
  // Am Turn-Limit hängt der Adapter einen zweiten `claude`-Prozess an (er holt
  // den Übergabe-Stand per --resume), und dessen Zeilen laufen durch denselben
  // Callback. Die Gruppe trägt dann ZWEI result-Zeilen — die erste vom
  // abgebrochenen Lauf. Nur die Summe beschreibt, was der Sub-Lauf insgesamt
  // gekostet hat, und gescheitert ist er, sobald eine davon einen Fehler meldet.
  const results = group
    .map((e) => (typeof e.payload === "object" && e.payload ? (e.payload as Record<string, any>) : {}))
    .filter((p) => p.type === "result");
  const sum = (pick: (p: Record<string, any>) => unknown) =>
    results.reduce((n, p) => n + (Number(pick(p)) || 0), 0);
  const cost = sum((p) => p.total_cost_usd);
  const inTok = sum((p) => p.usage?.input_tokens);
  const outTok = sum((p) => p.usage?.output_tokens);
  const turns = sum((p) => p.num_turns);

  const tools = inner.reduce(
    (n, it) => n + (it.kind === "turn" ? it.rows.filter((r) => r.type === "tool").length : 0),
    0,
  );
  const ms = new Date(last.created_at).getTime() - new Date(first.created_at).getTime();
  const meta = [
    tools ? i18n.t("activity.subAgentTools", { count: tools }) : "",
    turns ? i18n.t("activity.subAgentTurns", { count: turns }) : "",
    ms > 0 ? fmtDelta(ms) : "",
    cost ? fmtUSD(cost) : "",
    inTok ? `${inTok} → ${outTok} Tokens` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  return {
    key: `sub-${first.id}`,
    kind: "subagent",
    dir: mark.dir,
    // Der Auftrag steht nur auf der ersten Zeile — von dort einsammeln.
    task: group.map(subAgentMark).find((m) => m?.task)?.task,
    time: fmtTime(first.created_at),
    meta,
    state: results.length === 0 ? "running" : results.some((p) => p.is_error) ? "failed" : "ok",
    items: newestFirst(inner),
  };
}

function newestFirst(items: FeedItem[]): FeedItem[] {
  const groups: FeedItem[][] = [];
  for (const it of items) {
    if (it.kind === "day" || groups.length === 0) groups.push([]);
    groups[groups.length - 1].push(it);
  }
  return groups
    .reverse()
    .flatMap((g) => (g[0]?.kind === "day" ? [g[0], ...g.slice(1).reverse()] : g.reverse()));
}

// --- Rendering ---

function ToolRow({ call }: { call: ToolCall }) {
  const { t } = useTranslation();
  const pill = call.pending ? (
    <span className="pill mut">
      {call.elapsedSeconds
        ? t("activity.toolRunningFor", { time: fmtDelta(call.elapsedSeconds * 1000) })
        : t("activity.toolPending")}
    </span>
  ) : call.isError ? (
    <span className="pill err">{t("activity.toolError")}</span>
  ) : (
    <span className="pill ok">{t("activity.toolOk")}</span>
  );
  const hasBody = call.detail || call.result || call.input != null || call.imageURL;
  // Screenshots standardmäßig aufgeklappt zeigen — das Bild ist der Kern der Aktion.
  return (
    <details className="tool" open={Boolean(call.imageURL)}>
      <summary>
        <span className="lft">
          <span className="chev">▸</span>
          <span className="tool-name">{call.name}</span>
          {call.detail && <span className="tool-detail">{truncate(call.detail, 90)}</span>}
          {call.imageURL && <span className="tool-shot-badge">◱</span>}
        </span>
        {pill}
      </summary>
      {hasBody && (
        <div className="tool-body">
          {call.imageURL && (
            <div className="tool-sec">
              <span className="tool-sec-l">{t("activity.screenshot")}</span>
              <a href={call.imageURL} target="_blank" rel="noopener noreferrer">
                <img className="tool-shot" src={call.imageURL} alt={t("activity.screenshot")} loading="lazy" />
              </a>
            </div>
          )}
          {call.input != null && (
            <div className="tool-sec">
              <span className="tool-sec-l">{t("activity.toolCall")}</span>
              <pre>{truncate(JSON.stringify(call.input, null, 2), 2000)}</pre>
            </div>
          )}
          {call.result != null && (
            <div className="tool-sec">
              <span className="tool-sec-l">{call.isError ? t("activity.toolError") : t("activity.toolResult")}</span>
              <pre>{truncate(call.result, 2000)}</pre>
            </div>
          )}
        </div>
      )}
    </details>
  );
}

// SubRun: der Sub-Lauf als ein zugeklappter Block. Der Kopf trägt Checkout,
// Auftrag und Kennzahlen — damit beantwortet er die Frage „wer hat das getan
// und wozu?", ohne dass man aufklappen muss.
function SubRun({ item }: { item: Extract<FeedItem, { kind: "subagent" }> }) {
  const { t } = useTranslation();
  return (
    <details className="subrun">
      <summary>
        <span className="subrun-head">
          <span className="chev">▸</span>
          <Icon name="layers" />
          <span className="subrun-name">
            {t("activity.subAgentTitle", { dir: shortDir(item.dir) })}
          </span>
          <span className="subrun-time">{item.time}</span>
          {item.state === "running" && <span className="pill mut">{t("activity.subAgentRunning")}</span>}
          {item.state === "ok" && <span className="pill ok">{t("activity.toolOk")}</span>}
          {item.state === "failed" && <span className="pill err">{t("activity.toolError")}</span>}
        </span>
        {item.task && <span className="subrun-task">„{truncate(item.task, 220)}"</span>}
        {item.meta && <span className="subrun-meta">{item.meta}</span>}
      </summary>
      <div className="subrun-body">
        <FeedItems items={item.items} />
      </div>
    </details>
  );
}

// PhaseRow ist die Zeile für eine Phase der Plattform: Image holen, Home
// herstellen, Home sichern. Solange sie läuft, trägt sie einen Balken — bei
// bekannter Gesamtgröße einen echten, sonst einen laufenden ohne Anspruch auf
// eine Prozentzahl. Ist sie durch, bleibt sie als Zeile mit ihren Zahlen
// stehen: „3,4 GB in 4 min" ist hinterher die Auskunft, die zählt.
// phasenAnteil: der Balken bekommt nur dann eine Länge, wenn die Phase ihr
// eigenes Ende kennt. Ein Bild kennt seine Größe, ein Sync erst, wenn er durch
// ist — und ein Balken, der eine Zahl behauptet, die niemand hat, ist schlimmer
// als keiner.
export function phasenAnteil(item: Extract<FeedItem, { kind: "phase" }>): number | undefined {
  if (item.bytesTotal && item.bytes !== undefined) return Math.min(1, item.bytes / item.bytesTotal);
  if (item.countTotal && item.count !== undefined) return Math.min(1, item.count / item.countTotal);
  return undefined;
}

function PhaseRow({ item }: { item: Extract<FeedItem, { kind: "phase" }> }) {
  const { t } = useTranslation();
  const icons: Record<string, IconName> = {
    image: "download",
    home: "download",
    home_sync: "save",
    home_synced: "save",
  };
  const label = t(`activity.phase.${item.phase}${item.done ? "Done" : ""}`, item.phase);
  const anteil = phasenAnteil(item);
  const zahlen: string[] = [];
  if (item.bytes !== undefined && item.bytesTotal) {
    zahlen.push(`${fmtBytes(item.bytes)} / ${fmtBytes(item.bytesTotal)}`);
  } else if (item.bytes !== undefined) {
    zahlen.push(fmtBytes(item.bytes));
  }
  if (item.count !== undefined) {
    zahlen.push(
      item.countTotal
        ? t("activity.phase.filesOf", { count: fmtCount(item.count), total: fmtCount(item.countTotal) })
        : t("activity.phase.files", { count: fmtCount(item.count) }),
    );
  }
  if (item.ms !== undefined) zahlen.push(fmtDelta(item.ms));

  if (item.error) {
    return (
      <div className="act-phase failed">
        <div className="ph">
          <Icon name="x" />
          <b>{t(`activity.phase.${item.phase}Failed`, label)}</b>
          <span className="act-meta">{item.time}</span>
        </div>
        <p className="act-phase-error">{item.error}</p>
      </div>
    );
  }

  return (
    <div className={`act-phase ${item.done ? "done" : "running"}`}>
      <div className="ph">
        <Icon name={icons[item.phase] ?? "info"} />
        <b>{label}</b>
        {item.detail && <code className="act-phase-detail">{item.detail}</code>}
        <span className="act-meta">
          {zahlen.length > 0 && `${zahlen.join(" · ")} · `}
          {item.time}
        </span>
      </div>
      {!item.done && (
        <div className={`act-bar ${anteil === undefined ? "unknown" : ""}`}>
          <span style={anteil === undefined ? undefined : { width: `${Math.round(anteil * 100)}%` }} />
        </div>
      )}
    </div>
  );
}

// FeedItems rendert eine Item-Liste. Eigene Komponente, weil der Sub-Lauf-Block
// sie für seinen Inhalt erneut braucht.
function FeedItems({ items }: { items: FeedItem[] }) {
  const { t } = useTranslation();
  return (
    <>
      {items.map((it) => {
        switch (it.kind) {
          case "day":
            return (
              <div key={it.key} className="act-day">
                <span>{it.text}</span>
              </div>
            );
          case "evt":
            return (
              <div key={it.key} className={`evt ${it.tone ?? ""}`}>
                <Icon name={it.icon} /> {it.text}
                {it.count && it.count > 1 ? ` (×${it.count})` : ""} · {it.time}
              </div>
            );
          case "phase":
            return <PhaseRow key={it.key} item={it} />;
          case "gate":
            return (
              <div key={it.key} className={`gate ${it.tone}`}>
                <Icon name={it.icon} /> {it.text} · {it.time}
              </div>
            );
          case "parked":
            return (
              <div key={it.key} className="parked">
                <div className="ph">
                  <Icon name="clock" /> <b>{it.title}</b>
                </div>
                <p>{it.text}</p>
              </div>
            );
          case "result":
            return (
              <div key={it.key} className={`act-result ${it.ok ? "" : "err"}`}>
                <div className="ph">
                  <Icon name={it.ok ? "check" : "x"} />
                  <b>{it.ok ? t("activity.result") : t("activity.failed")}</b>
                  <span className="act-meta">
                    {it.meta && `${it.meta} · `}
                    {it.time}
                  </span>
                </div>
                <p className="voice">{truncate(it.text, 1200)}</p>
              </div>
            );
          case "turn":
            return (
              <div key={it.key} className="turn">
                <div className="turn-h">
                  <span className="turn-dot" />
                  <span className="lbl">{it.time}</span>
                </div>
                {it.voice.map((v, j) => (
                  <p key={j} className="voice">
                    {v}
                  </p>
                ))}
                {it.rows.map((r, j) =>
                  r.type === "tool" ? (
                    <ToolRow key={r.call.key} call={r.call} />
                  ) : (
                    <div key={j} className={`gate in-turn ${r.gate.tone}`}>
                      <Icon name={r.gate.icon} /> {r.gate.text} · {r.gate.time}
                    </div>
                  ),
                )}
              </div>
            );
          case "subagent":
            return <SubRun key={it.key} item={it} />;
          default:
            return null;
        }
      })}
    </>
  );
}

export function ActivityFeed({ events, truncated }: { events: RecordingEvent[]; truncated?: boolean }) {
  const { t } = useTranslation();
  return (
    <div className="act-feed">
      <FeedItems items={newestFirst(buildFeed(events))} />
      {truncated && (
        <div className="evt muted">
          <Icon name="info" /> {t("activity.truncatedFeed", { count: events.length })}
        </div>
      )}
    </div>
  );
}
