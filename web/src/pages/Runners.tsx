import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, post, type Principal } from "../api";

// Die Runner-Ansicht (spec/16, Stufe 5). Ab dem dritten Runner ist sie das,
// was den Betrieb bedienbar macht: welche Hosts es gibt, welcher gerade traegt,
// und wo der Platz knapp wird — bevor er alle ist, nicht danach.

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
  logical_bytes: number;
  agents: number;
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

/* embedded: siehe Runtimes — der Reiter trägt die Überschrift. */
export default function Runners({ me, embedded = false }: { me: Principal; embedded?: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  /* Dieselben Rollen wie im Server (httpapi: manage). Hier stand
     "platform_admin" — die oberste Org-Rolle heisst seit Migration 0061
     org_admin, und weil sie niemand mehr traegt, war die Antwort immer nein:
     Die Karte zum Registrieren eines Runners war damit fuer alle weg, auch
     fuer die, die den Endpunkt dahinter benutzen duerfen. */
  const manage = me.Role === "org_admin" || me.Role === "agent_owner";
  const [token, setToken] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [cleanup, setCleanup] = useState<CleanupView | null>(null);

  const runners = useQuery({
    queryKey: ["runners"],
    queryFn: () => api<RunnerView[]>("/runners"),
    // Der Live-Teil (verbunden, laufende Sandboxen, freier Platz) ist nur so
    // lange wahr, wie die Verbindung steht — deshalb nachladen statt einmal
    // holen.
    refetchInterval: 10_000,
  });
  // Was zwischen den Runnern dieser Organisation und einer laufenden Sandbox
  // steht. Die Pruefung gab es vorher schon — sie lief beim Start ins Log und
  // in die Onboarding-Ansicht, die verschwindet, sobald die fuenf Schritte
  // erledigt sind. Genau deshalb meldete eine seit Wochen laufende Instanz
  // nichts, als ihre Datenebene ausfiel.
  const health = useQuery({
    queryKey: ["runner-health"],
    queryFn: () => api<{ ready: boolean; problems: string[] }>("/runners/health"),
    refetchInterval: 30_000,
  });
  const store = useQuery({
    queryKey: ["home-store"],
    queryFn: () => api<StoreView>("/platform/home-store"),
  });

  const createToken = useMutation({
    mutationFn: () => post<{ token: string }>("/runners/registration-tokens", {}),
    onSuccess: (r) => setToken(r.token),
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
                    {r.live?.connected ? (
                      <span className="pill ok">{t("runners.connected")}</span>
                    ) : (
                      <>
                        <span className="pill mut" title={t("runners.offlineHint")}>
                          {t("runners.offline")}
                        </span>
                        {/* Bei einem Runner, der weg ist, ist „seit wann" die
                            eigentliche Auskunft — ein Wartungsfenster liest
                            sich anders als ein Host, der seit Tagen nicht mehr
                            gesehen wurde. */}
                        {r.last_seen_at && (
                          <div className="muted text-xs" style={{ marginTop: 2 }}>
                            {t("runners.lastSeen", { when: ago(r.last_seen_at, t) })}
                          </div>
                        )}
                      </>
                    )}
                    {/* Versionsversatz wird benannt, nicht bloss geduldet:
                        Runner und Server werden getrennt ausgeliefert. */}
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
                    {/* Der Name ist auch ein Link, aber ein Link in einer
                        Tabellenzelle sieht aus wie Text: „Bearbeiten" steht
                        dort, wo bei jeder anderen Zeile die Handlung steht.
                        Der eingebaute Runner hat sie auch — Name, Tags und
                        Arbeitsplätze gelten für ihn genauso; was ihm fehlt,
                        ist das Löschen. */}
                    {manage && (
                      <Link className="btn sm" to={`/infrastructure/runners/${r.id}`}>
                        {t("runners.edit")}
                      </Link>
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
              {/* Einmal im Klartext, danach nur noch als Hash — deshalb zum
                  Kopieren und nicht zum Abtippen. Der Befehl ist so gebaut,
                  dass er sich einfügen lässt: kein Platzhalter, den jemand
                  versehentlich mit überträgt. */}
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
        </div>
      )}

      {store.data?.enabled && (
        <div className="card p-4 flex flex-col gap-3">
          <h2 className="text-[15px]">{t("runners.storeTitle")}</h2>
          <p className="muted text-xs">{t("runners.storeHint")}</p>
          <div className="text-sm" style={{ marginTop: 4 }}>
            {t("runners.storeSize", {
              size: formatBytes(store.data.bytes),
              agents: store.data.agents,
            })}
          </div>
          {/* The comparison is the explanation: the homes together weigh a
              multiple of what the store occupies, because the toolchain caches
              are byte-for-byte identical on every developer home and therefore
              lie there once. Without this line the store is a
              Verzeichnis, das aus unsichtbaren Gruenden waechst. */}
          {store.data.logical_bytes > 0 && store.data.bytes > 0 && (
            <div className="text-sm">
              {/* Unter 1,1× ist „x-mal kleiner" albern — und der Speicher liegt
                  sogar leicht darueber, weil die Manifeste selbst Bloecke sind.
                  Dann sagt der Satz, wann die Ersparnis kommt, statt eine zu
                  behaupten, die es nicht gibt. */}
              {store.data.logical_bytes / store.data.bytes >= 1.1
                ? t("runners.storeDedup", {
                    logical: formatBytes(store.data.logical_bytes),
                    factor: (store.data.logical_bytes / store.data.bytes).toFixed(1),
                  })
                : t("runners.storeNoDedupYet", { logical: formatBytes(store.data.logical_bytes) })}
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
                    freed: formatBytes(cleanup.freed_bytes),
                  })
                : t("runners.cleanupDone", {
                    blocks: cleanup.blocks_removed,
                    freed: formatBytes(cleanup.freed_bytes),
                  })}
            </p>
          )}
          <p className="muted text-xs">{t("runners.cleanupAutomatic")}</p>
        </div>
      )}
    </div>
  );
}

// registerCommand ist, was auf dem neuen Host laufen muss — als Ganzes zum
// Einfügen, und zwar ab dem Punkt, an dem dieser Host wirklich steht: ohne
// covey-runner. Der Befehl, der ein Binary voraussetzt, das es dort nicht gibt,
// ist einer, nach dem man erst noch suchen muss.
//
// Das Installationsskript kommt von dieser Instanz und bringt die zu ihr
// passende Version mit (spec/16, „Protokollversion") — deshalb die eigene
// Adresse und nicht die von GitHub.
//
// Ohne Beschreibung und Tags: beides ist optional, und ein Platzhalter im
// Befehl ist etwas, das jemand mitkopiert und dann sucht, warum sein Runner
// „…" heißt.
function registerCommand(token: string): string {
  const origin = window.location.origin;
  return [
    `curl -fsSL ${origin}/install.sh | sh -s -- --runner`,
    `covey-runner register --url ${origin} --token ${token}`,
    `covey-runner run`,
  ].join("\n");
}

// ago ist „vor …" in grober Koernung. Genauer waere unnuetz: bei einem Runner,
// der weg ist, entscheidet die Groessenordnung — Minuten sind ein Neustart,
// Tage sind ein Host, um den sich niemand mehr kuemmert.
function ago(iso: string, t: (k: string, o?: Record<string, unknown>) => string): string {
  const seconds = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 90) return t("runners.agoJustNow");
  if (seconds < 3600) return t("runners.agoMinutes", { count: Math.round(seconds / 60) });
  if (seconds < 86400) return t("runners.agoHours", { count: Math.round(seconds / 3600) });
  return t("runners.agoDays", { count: Math.round(seconds / 86400) });
}

// DiskBar zeigt den Fuellstand des Dateisystems, auf dem die Arbeitskopien
// liegen — genau die Zahl, die entscheidet, ob das naechste Home noch passt.
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
      <div className="muted text-xs">{t("runners.free", { size: formatBytes(free) })}</div>
    </div>
  );
}
