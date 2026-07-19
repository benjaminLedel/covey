import { statusLabel, type RecordingEvent } from "../api";

// ActivityFeed: übersetzt das lückenlose Recording in eine erzählende
// Aktivitätsansicht im Stil des Mockups — Turns mit der Stimme des Agenten
// (Lora), aufklappbare Tool-Aufrufe mit Status-Pill, Ereignis- und
// Gate-Zeilen für Lifecycle/Credential/Approval/Guardrail sowie ein
// Parked-Block, wenn der Agent auf ein externes Ereignis wartet.
// Tool-Aufruf und Tool-Ergebnis werden über die tool_use_id korreliert,
// sodass Aufruf und Antwort als ein Eintrag erscheinen.

type Tone = "ok" | "warn" | "danger" | "muted";

type ToolCall = {
  key: string;
  name: string;
  detail: string; // kompakte Argument-Vorschau im Summary
  input?: unknown;
  result?: string;
  isError?: boolean;
  pending: boolean;
};

type GateData = { icon: IconName; text: string; time: string; tone: Tone };

// Ein Turn besteht aus der Stimme des Agenten plus einer chronologischen
// Folge von Tool-Aufrufen und Gate-Zeilen (Freigaben, Credentials) — so
// bleibt ein Arbeitsschritt als zusammenhängender Block lesbar, statt an
// jeder Freigabe zu zerfallen.
type TurnRow = { type: "tool"; call: ToolCall } | { type: "gate"; gate: GateData };

// key: stabile Identität aus der Event-ID — das Fenster der letzten N Events
// verschiebt sich beim Live-Follow, mit Index-Keys würde React dann alles
// neu mounten (Scroll springt, aufgeklappte Details schließen sich).
type FeedItem = { key: string } & (
  | { kind: "day"; text: string }
  | { kind: "evt"; icon: IconName; text: string; time: string; tone?: Tone; count?: number }
  | { kind: "turn"; time: string; voice: string[]; rows: TurnRow[] }
  | { kind: "gate"; icon: IconName; text: string; time: string; tone: Tone }
  | { kind: "parked"; title: string; text: string; time: string }
  | { kind: "result"; ok: boolean; text: string; meta: string; time: string }
);

// --- Icons: kleine Inline-SVGs im Strichstil der übrigen UI ---

type IconName = "bolt" | "moon" | "check" | "shield" | "key" | "clock" | "x" | "flag" | "info";

const paths: Record<IconName, string> = {
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
  new Date(iso).toLocaleTimeString("de-DE", { hour: "2-digit", minute: "2-digit" });

const fmtDay = (iso: string) =>
  new Date(iso).toLocaleDateString("de-DE", { day: "numeric", month: "long", year: "numeric" });

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
  // Gate-Zeilen laufen in den offenen Turn hinein, damit ein Arbeitsschritt
  // nicht an jeder Freigabe auseinanderbricht; ohne Turn stehen sie einzeln.
  const pushGate = (gate: GateData, key: string) => {
    if (turn) turn.rows.push({ type: "gate", gate });
    else items.push({ key, kind: "gate", ...gate });
  };
  // pushEvt fasst direkt aufeinanderfolgende identische Zeilen zusammen
  // ("Sitzung gestartet ×3") — Wiederanläufe erzeugen sonst Rauschen.
  const pushEvt = (evt: Extract<FeedItem, { kind: "evt" }>) => {
    const last = items[items.length - 1];
    if (last?.kind === "evt" && last.text === evt.text && last.tone === evt.tone) {
      last.count = (last.count ?? 1) + 1;
      last.time = evt.time;
      return;
    }
    items.push(evt);
  };

  for (const e of events) {
    const p = (typeof e.payload === "object" && e.payload !== null ? e.payload : {}) as Record<
      string,
      any
    >;
    const time = fmtTime(e.created_at);
    const day = fmtDay(e.created_at);
    const k = `e${e.id}`;
    if (day !== lastDay) {
      lastDay = day;
      closeTurn();
      items.push({ key: `day-${day}`, kind: "day", text: day });
    }

    switch (e.kind) {
      case "runtime": {
        // Mock-Runtime: schlichte {text}-Events als Stimme des Agenten.
        if (typeof p.text === "string" && !p.message && !p.type) {
          ensureTurn(time, k).voice.push(p.text);
          break;
        }
        switch (p.type) {
          case "system":
            closeTurn();
            pushEvt({
              key: k,
              kind: "evt",
              icon: "info",
              text: `Sitzung gestartet${p.model ? ` · Modell ${p.model}` : ""}`,
              time,
              tone: "muted",
            });
            break;
          case "rate_limit_event":
            break; // Rauschen — im Rohmodus weiter sichtbar
          case "assistant": {
            const blocks: any[] = Array.isArray(p.message?.content) ? p.message.content : [];
            for (const b of blocks) {
              if (b.type === "text" && b.text) {
                // Ein neuer Gedanke beginnt einen neuen Turn, sobald der
                // aktuelle schon Inhalt trägt.
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
              p.total_cost_usd ? `$${Number(p.total_cost_usd).toFixed(4)}` : "",
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
              text: String(p.result ?? (p.is_error ? "Lauf fehlgeschlagen" : "Lauf beendet")),
              meta,
              time,
            });
            break;
          }
          default:
            // Unbekannte Runtime-Zeile: nicht verschlucken, aber leise.
            items.push({
              key: k,
              kind: "evt",
              icon: "info",
              text: truncate(JSON.stringify(e.payload), 160),
              time,
              tone: "muted",
            });
        }
        break;
      }

      case "lifecycle": {
        closeTurn();
        const status = String(p.status ?? "");
        if (status === "blocked") {
          items.push({
            key: k,
            kind: "parked",
            title: "Wartet auf externes Ereignis",
            text: `${p.question ? `${p.question} ` : ""}${
              p.correlation_key ? `Wacht auf bei „${p.correlation_key}". ` : ""
            }Geparkt seit ${time}.`,
            time,
          });
        } else if (status === "wake_on_correlation") {
          items.push({
            key: k,
            kind: "evt",
            icon: "bolt",
            text: `Geweckt — das erwartete Ereignis ist eingetreten${
              p.correlation_key ? ` („${p.correlation_key}")` : ""
            }`,
            time,
          });
        } else if (status === "working" || status === "triggered" || status === "triage") {
          items.push({ key: k, kind: "evt", icon: "bolt", text: "Geweckt — beginnt zu arbeiten", time });
        } else if (status === "sleeping") {
          items.push({
            key: k,
            kind: "evt",
            icon: "moon",
            text: "Schläft — wartet auf neue Arbeit",
            time,
            tone: "muted",
          });
        } else if (status === "task_done") {
          items.push({ key: k, kind: "gate", icon: "check", text: "Aufgabe abgeschlossen", time, tone: "ok" });
        } else if (status === "task_failed") {
          items.push({ key: k, kind: "gate", icon: "x", text: "Aufgabe fehlgeschlagen", time, tone: "danger" });
        } else if (status === "task_blocked") {
          items.push({
            key: k,
            kind: "gate",
            icon: "clock",
            text: "Aufgabe wartet auf ein externes Ereignis",
            time,
            tone: "warn",
          });
        } else if (status === "stage") {
          items.push({ key: k, kind: "evt", icon: "flag", text: `Etappe erreicht: ${p.stage}`, time });
        } else if (status === "killed") {
          items.push({ key: k, kind: "gate", icon: "x", text: "Agent gestoppt (Kill-Switch)", time, tone: "danger" });
        } else {
          items.push({
            key: k,
            kind: "evt",
            icon: "info",
            text: `Status → ${statusLabel[status] ?? status}`,
            time,
          });
        }
        break;
      }

      case "credential": {
        if (p.granted) {
          const ttl = p.ttl_secs ? ` · gültig ${Math.round(p.ttl_secs / 60)} min` : "";
          pushGate({
            icon: "key",
            text: `Zugang zu ${p.system} kurzlebig ausgestellt${p.proactive ? " (proaktiv)" : ""}${ttl}`,
            time,
            tone: "ok",
          }, k);
        } else {
          pushGate({
            icon: "key",
            text: `Zugang zu ${p.system} verweigert${p.reason ? ` — ${p.reason}` : ""}`,
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
            text: `Freigabe angefragt: ${p.action} — wartet auf Entscheidung`,
            time,
            tone: "warn",
          }, k);
        } else if (d === "denied") {
          pushGate({ icon: "x", text: `Freigabe verweigert: ${p.action}`, time, tone: "danger" }, k);
        } else {
          const how = d === "auto-allow" ? "automatisch erlaubt" : "erteilt";
          pushGate({ icon: "shield", text: `Freigabe ${how}: ${p.action}`, time, tone: "ok" }, k);
        }
        break;
      }

      case "guardrail": {
        const what = p.action ?? p.system ?? "";
        pushGate({
          icon: "shield",
          text: `Guard-Rail ${p.rule ?? ""} ausgelöst${what ? `: ${what}` : ""}${
            p.pattern ? ` (Muster „${p.pattern}")` : ""
          }`,
          time,
          tone: "danger",
        }, k);
        break;
      }

      case "action": {
        // Aktion im Zielsystem über den Action-Proxy — als Tool-Zeile.
        const call: ToolCall = {
          key: `action-${e.id}`,
          name: `Zielsystem · ${p.action ?? "Aktion"}`,
          detail: toolDetail("", p.params ?? p),
          input: p,
          pending: false,
          isError: p.ok === false,
          result: p.result != null ? resultText(p.result) : undefined,
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

// newestFirst dreht die chronologisch gebauten Items für die Anzeige um:
// jüngster Tag zuerst, innerhalb eines Tages jüngster Eintrag zuerst. Die
// Tageszeile bleibt dabei über ihren Einträgen stehen.
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
  const pill = call.pending ? (
    <span className="pill mut">läuft …</span>
  ) : call.isError ? (
    <span className="pill err">Fehler</span>
  ) : (
    <span className="pill ok">ok</span>
  );
  const hasBody = call.detail || call.result || call.input != null;
  return (
    <details className="tool">
      <summary>
        <span className="lft">
          <span className="chev">▸</span>
          <span className="tool-name">{call.name}</span>
          {call.detail && <span className="tool-detail">{truncate(call.detail, 90)}</span>}
        </span>
        {pill}
      </summary>
      {hasBody && (
        <div className="tool-body">
          {call.input != null && (
            <div className="tool-sec">
              <span className="tool-sec-l">Aufruf</span>
              <pre>{truncate(JSON.stringify(call.input, null, 2), 2000)}</pre>
            </div>
          )}
          {call.result != null && (
            <div className="tool-sec">
              <span className="tool-sec-l">{call.isError ? "Fehler" : "Ergebnis"}</span>
              <pre>{truncate(call.result, 2000)}</pre>
            </div>
          )}
        </div>
      )}
    </details>
  );
}

export function ActivityFeed({ events, truncated }: { events: RecordingEvent[]; truncated?: boolean }) {
  const items = newestFirst(buildFeed(events));
  return (
    <div className="act-feed">
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
                  <b>{it.ok ? "Ergebnis" : "Fehlgeschlagen"}</b>
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
          default:
            return null;
        }
      })}
      {truncated && (
        <div className="evt muted">
          <Icon name="info" /> Ältere Ereignisse ausgeblendet — der Feed zeigt die jüngsten{" "}
          {events.length} Einträge.
        </div>
      )}
    </div>
  );
}
