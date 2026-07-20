import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api, post, ApiError, statusLabel, type Agent, type Principal } from "../api";
import { generateAgentName } from "../names";

const canManage = (role: string) => role === "platform_admin" || role === "agent_owner";
const canSecurity = (role: string) => role === "platform_admin" || role === "security";

export default function Dashboard({ me }: { me: Principal }) {
  const qc = useQueryClient();
  const agents = useQuery({ queryKey: ["agents"], queryFn: () => api<Agent[]>("/agents") });
  const fleet = useQuery({
    queryKey: ["fleet"],
    queryFn: () => api<{ fleet_killed: boolean }>("/fleet"),
  });
  const [showCreate, setShowCreate] = useState(false);
  const [showImport, setShowImport] = useState(false);

  const fleetMut = useMutation({
    mutationFn: (kill: boolean) => post(kill ? "/fleet/kill" : "/fleet/resume"),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["fleet"] });
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
  });

  const fleetKilled = fleet.data?.fleet_killed ?? false;

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-4">
        <h1 className="text-[22px]">Agenten</h1>
        <span className="muted">{agents.data?.length ?? 0} in der Organisation</span>
        <span className="ml-auto" />
        {canManage(me.Role) && (
          <button className="btn" onClick={() => setShowCreate((v) => !v)}>
            + Agent anlegen
          </button>
        )}
        {canManage(me.Role) && (
          <button className="btn" onClick={() => setShowImport((v) => !v)} title="Agent aus JSON-Bundle importieren">
            Import
          </button>
        )}
        {canSecurity(me.Role) &&
          (fleetKilled ? (
            <button className="btn" onClick={() => fleetMut.mutate(false)}>
              Notaus lösen
            </button>
          ) : (
            <button className="btn danger" onClick={() => fleetMut.mutate(true)} title="Alle Agenten sofort stoppen">
              Notaus (Flotte)
            </button>
          ))}
      </div>

      {fleetKilled && (
        <div
          className="card mb-4"
          style={{ borderColor: "var(--border-danger)", color: "var(--text-danger)" }}
        >
          Flottenweiter Kill-Switch aktiv — kein Agent wird geweckt, bis der Notaus gelöst wird.
        </div>
      )}

      {showCreate && <CreateAgent onDone={() => setShowCreate(false)} />}
      {showImport && <ImportAgent />}

      <div className="grid gap-3" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(250px, 1fr))" }}>
        {agents.data?.map((a) => (
          <AgentCard key={a.id} agent={a} />
        ))}
        {agents.data?.length === 0 && (
          <p className="muted">Noch keine Agenten — legen Sie den ersten an.</p>
        )}
      </div>
    </div>
  );
}

function AgentCard({ agent }: { agent: Agent }) {
  const initials = agent.display_name
    .split(/\s+/)
    .map((w) => w[0])
    .join("")
    .slice(0, 2)
    .toUpperCase();
  return (
    <Link to={`/agents/${agent.id}`} className="card block no-underline" style={{ color: "inherit" }}>
      <div className="flex items-center justify-between mb-3">
        <div className="flex items-center gap-2.5">
          <div className="avatar">{initials}</div>
          <div>
            <div className="font-medium text-sm">{agent.display_name}</div>
            <div className="muted text-xs">{agent.slug}</div>
          </div>
        </div>
        <span className={`badge st-${agent.killed ? "killed" : agent.status}`}>
          {agent.killed ? "gestoppt" : statusLabel[agent.status] ?? agent.status}
        </span>
      </div>
      <div className="muted text-xs">
        Runtime: <span className="mono">{agent.runtime}</span>
        {agent.budget_usd > 0 && <> · Budget {agent.budget_usd.toFixed(2)} $</>}
      </div>
    </Link>
  );
}

function CreateAgent({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [slug, setSlug] = useState("");
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState("claude-code");
  const mut = useMutation({
    mutationFn: () => post("/agents", { slug, display_name: name, runtime }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["agents"] });
      onDone();
    },
  });
  return (
    <form
      className="card mb-4 flex gap-3 items-end flex-wrap"
      onSubmit={(e) => {
        e.preventDefault();
        mut.mutate();
      }}
    >
      <div className="flex-1 min-w-40">
        <label>Name</label>
        <div className="flex gap-2">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Support-Agent" required />
          <button
            type="button"
            className="btn"
            title="Lustigen Namen würfeln"
            onClick={() => {
              const g = generateAgentName();
              setName(g.name);
              setSlug(g.slug);
            }}
          >
            🎲
          </button>
        </div>
      </div>
      <div className="flex-1 min-w-40">
        <label>Slug</label>
        <input value={slug} onChange={(e) => setSlug(e.target.value)} placeholder="support" required />
      </div>
      <div className="min-w-36">
        <label>Runtime</label>
        <select value={runtime} onChange={(e) => setRuntime(e.target.value)}>
          <option value="claude-code">claude-code</option>
          <option value="mock">mock</option>
        </select>
      </div>
      <button className="btn primary" disabled={mut.isPending}>
        Anlegen
      </button>
      {mut.isError && <span className="danger-text text-xs">{String(mut.error)}</span>}
    </form>
  );
}

// Import eines Agenten aus einem Export-Bundle (JSON). Bei Slug-Kollision
// fragt die Karte nach einem neuen Slug und importiert erneut (?slug=).
function ImportAgent() {
  const qc = useQueryClient();
  const fileRef = useRef<HTMLInputElement>(null);
  const [bundle, setBundle] = useState<{ agent?: { slug?: string } } | null>(null);
  const [fileName, setFileName] = useState("");
  const [slugOverride, setSlugOverride] = useState("");
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useState("");
  const [result, setResult] = useState<{ agent: Agent; warnings: string[] } | null>(null);

  const mut = useMutation({
    mutationFn: (args: { bundle: unknown; slug?: string }) =>
      post<{ agent: Agent; warnings: string[] }>(
        `/agents/import${args.slug ? `?slug=${encodeURIComponent(args.slug)}` : ""}`,
        args.bundle,
      ),
    onSuccess: (res) => {
      setResult(res);
      setConflict(false);
      setError("");
      qc.invalidateQueries({ queryKey: ["agents"] });
    },
    onError: (err) => {
      if (err instanceof ApiError && err.status === 409) {
        setConflict(true);
        setError(err.message);
      } else {
        setConflict(false);
        setError(String(err instanceof ApiError ? err.message : err));
      }
    },
  });

  const pick = async (f: File | undefined) => {
    if (!f) return;
    setResult(null);
    setConflict(false);
    setError("");
    setSlugOverride("");
    setFileName(f.name);
    try {
      const parsed = JSON.parse(await f.text());
      setBundle(parsed);
      mut.mutate({ bundle: parsed });
    } catch {
      setBundle(null);
      setError("Datei ist kein gültiges JSON-Bundle.");
    }
  };

  return (
    <div className="card mb-4">
      <div className="flex gap-3 items-center flex-wrap">
        <button className="btn" type="button" onClick={() => fileRef.current?.click()} disabled={mut.isPending}>
          Bundle wählen …
        </button>
        <input
          ref={fileRef}
          type="file"
          accept="application/json,.json"
          style={{ display: "none" }}
          onChange={(e) => {
            pick(e.target.files?.[0]);
            e.target.value = "";
          }}
        />
        {fileName && <span className="muted text-xs mono">{fileName}</span>}
        {mut.isPending && <span className="muted text-xs">Importiere …</span>}
      </div>
      {conflict && bundle && (
        <form
          className="flex gap-2 items-end mt-3 flex-wrap"
          onSubmit={(e) => {
            e.preventDefault();
            if (slugOverride) mut.mutate({ bundle, slug: slugOverride });
          }}
        >
          <div className="min-w-40">
            <label>Neuer Slug</label>
            <input
              value={slugOverride}
              onChange={(e) => setSlugOverride(e.target.value)}
              placeholder={`${bundle.agent?.slug ?? "agent"}-2`}
              required
            />
          </div>
          <button className="btn primary" disabled={mut.isPending || !slugOverride}>
            Erneut importieren
          </button>
        </form>
      )}
      {error && <p className="danger-text text-xs mt-2 mb-0">{error}</p>}
      {result && (
        <div className="mt-3">
          <p className="text-sm mb-1">
            Importiert:{" "}
            <Link to={`/agents/${result.agent.id}`}>
              <b>{result.agent.display_name}</b> ({result.agent.slug})
            </Link>
          </p>
          {result.warnings.length > 0 && (
            <ul className="text-xs mb-0" style={{ color: "var(--text-warning, #b58900)", paddingLeft: "1.2em" }}>
              {result.warnings.map((wtext, i) => (
                <li key={i}>{wtext}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </div>
  );
}
