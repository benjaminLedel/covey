import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, post, type MCPTool, type Principal, type TargetPlugin } from "../api";
import { ConfirmDialog, Modal } from "../components/Modal";
import { TargetIcon, hasBrandMark } from "../components/TargetIcon";
import { TargetSetupWizard } from "./targets/SetupWizard";
import { CatalogTab } from "./targets/Catalog";

const kindKey: Record<TargetPlugin["kind"], string> = {
  builtin: "targets.kindBuiltin",
  custom: "targets.kindCustom",
  mcp: "targets.kindMcp",
};

// Kachelchen vor dem Namen: Logo bzw. Kategorie-Symbol des Zielsystems.
// Trägt es ein echtes Marken-Signet, bleibt die Kachel neutral, damit die
// Markenfarbe wirkt; sonst färbt sie wie bisher nach Plugin-Art.
function TargetMark({ plugin: p, lg }: { plugin: TargetPlugin; lg?: boolean }) {
  const brand = hasBrandMark(p.name);
  const cls = `tgt-mark${lg ? " lg" : ""}${brand ? " brand" : ` k-${p.kind}`}`;
  return (
    <span className={cls} aria-hidden="true">
      <TargetIcon name={p.name} kind={p.kind} category={p.category} size={lg ? 22 : 17} />
    </span>
  );
}

function hostOf(url?: string): string {
  if (!url) return "";
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
}

const exampleManifest = `{
  "name": "helpdesk",
  "label": "Helpdesk",
  "description": "Beispiel: REST-Zielsystem per Manifest",
  "auth": { "header": "X-API-Key", "format": "{token}" },
  "webhook": {
    "id_field": "issue.id",
    "event_id_field": "comment.id",
    "title_field": "issue.title",
    "body_field": "comment.text",
    "ignore_when": [{ "field": "comment.author_type", "equals": "agent" }]
  },
  "actions": {
    "get_issue": { "method": "GET", "path": "/issues/{issue_id}" },
    "comment": { "method": "POST", "path": "/issues/{issue_id}/comments" }
  },
  "prompt_doc": "Verfügbare helpdesk-Aktionen: get_issue {\\"issue_id\\":N}, comment {\\"issue_id\\":N,\\"text\\":\\"...\\"}."
}`;

// Reihenfolge der Kategorie-Chips; alles Unbekannte landet hinten unter
// "other". Welche Chips erscheinen, entscheiden die Daten — hier steht nur
// die Sortierung, keine Plugin-Liste.
const catOrder = ["ticketing", "code", "communication", "files", "web", "dev", "other"];
const catLabelKey = (c: string) => (catOrder.includes(c) ? `targets.cat.${c}` : "targets.cat.other");

// "available" = was diese Instanz hat (aktivierbar), "active" = eingeschaltet,
// "store" = der Katalog, aus dem installiert wird. Der Store ist die Auslage,
// nicht das Regal — deshalb traegt der Katalog den Namen.
type Tab = "available" | "active" | "store";

export default function Targets({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [tab, setTab] = useState<Tab>("available");
  const [query, setQuery] = useState("");
  const [cat, setCat] = useState("all");
  const [detail, setDetail] = useState<string | null>(null);
  const [wizard, setWizard] = useState<string | null>(null);
  const [confirmDel, setConfirmDel] = useState<TargetPlugin | null>(null);
  const [form, setForm] = useState<null | "manifest" | "mcp">(null);

  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });

  const canEdit = me.Role === "org_admin" || me.Role === "security";
  const invalidate = () => qc.invalidateQueries({ queryKey: ["targets"] });

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      patch(`/targets/${name}`, { enabled }),
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: (name: string) => del(`/targets/${name}`),
    onSuccess: () => {
      setConfirmDel(null);
      setDetail(null);
      invalidate();
    },
  });

  const discover = useMutation({
    mutationFn: ({ name, token }: { name: string; token?: string }) =>
      post(`/targets/${name}/discover`, token ? { token } : {}),
    onSuccess: invalidate,
  });

  const list = targets.data ?? [];
  const activeCount = list.filter((p) => p.enabled).length;

  // Kategorien samt Anzahl aus den Daten ableiten, in fester Reihenfolge.
  const cats = useMemo(() => {
    const counts = new Map<string, number>();
    for (const p of list) {
      const c = p.category && catOrder.includes(p.category) ? p.category : "other";
      counts.set(c, (counts.get(c) ?? 0) + 1);
    }
    return catOrder.filter((c) => counts.has(c)).map((c) => ({ cat: c, count: counts.get(c)! }));
  }, [list]);

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    return list
      .filter((p) => tab === "available" || p.enabled)
      .filter(
        (p) =>
          tab !== "available" ||
          cat === "all" ||
          (p.category && catOrder.includes(p.category) ? p.category : "other") === cat,
      )
      .filter(
        (p) =>
          !q ||
          p.name.toLowerCase().includes(q) ||
          (p.label || "").toLowerCase().includes(q) ||
          (p.description || "").toLowerCase().includes(q),
      )
      .sort(
        (a, b) =>
          Number(b.enabled) - Number(a.enabled) ||
          (a.label || a.name).localeCompare(b.label || b.name),
      );
  }, [list, query, cat, tab]);

  const open = detail ? list.find((p) => p.name === detail) ?? null : null;

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("targets.title")}</h1>
        <span className="muted">{t("targets.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 640 }}>
        {t("targets.desc")}
      </p>

      <nav className="subnav" role="tablist" aria-label={t("targets.title")}>
        {(["available", "active", "store"] as Tab[]).map((tb) => (
          <button
            key={tb}
            role="tab"
            aria-selected={tab === tb}
            className={tab === tb ? "active" : ""}
            onClick={() => setTab(tb)}
          >
            {tb === "available"
              ? t("targets.tabAvailable")
              : tb === "active"
                ? t("targets.tabActive", { count: activeCount })
                : t("targets.tabStore")}
          </button>
        ))}
      </nav>

      {(list.length > 0 || tab === "store") && (
        <div className="tgt-bar">
          <input
            className="tgt-search"
            type="search"
            placeholder={t("targets.search")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            aria-label={t("targets.search")}
          />
          {tab === "available" && (
            <div className="tgt-cats" role="group" aria-label={t("targets.filterLabel")}>
              <button
                className={`tgt-cat${cat === "all" ? " active" : ""}`}
                onClick={() => setCat("all")}
                aria-pressed={cat === "all"}
              >
                {t("targets.cat.all")} <span className="n">{list.length}</span>
              </button>
              {cats.map((c) => (
                <button
                  key={c.cat}
                  className={`tgt-cat${cat === c.cat ? " active" : ""}`}
                  onClick={() => setCat(c.cat)}
                  aria-pressed={cat === c.cat}
                >
                  {t(catLabelKey(c.cat))} <span className="n">{c.count}</span>
                </button>
              ))}
            </div>
          )}
          {tab !== "store" && (
            <span className="muted text-xs ml-auto">
              {t("targets.summary", { total: list.length, active: activeCount })}
            </span>
          )}
        </div>
      )}

      {tab === "store" && <CatalogTab canEdit={canEdit} query={query} />}

      {tab !== "store" && (
        <div className="tgt-grid">
          {shown.map((p) => (
          <TargetCard
            key={p.name}
            plugin={p}
            canEdit={canEdit}
            busy={toggle.isPending}
            onDetails={() => setDetail(p.name)}
            onSetup={() => setWizard(p.name)}
            onToggle={() => toggle.mutate({ name: p.name, enabled: !p.enabled })}
          />
          ))}
        </div>
      )}
      {wizard && <TargetSetupWizard name={wizard} onClose={() => setWizard(null)} />}
      {tab !== "store" && list.length > 0 && shown.length === 0 && (
        <p className="muted text-sm">
          {tab === "active" && !query ? (
            <>
              {t("targets.noneActive")}{" "}
              <button className="btn sm ml-2" onClick={() => setTab("available")}>
                {t("targets.toAvailable")}
              </button>
            </>
          ) : (
            t("targets.noMatch")
          )}
        </p>
      )}
      {tab !== "store" && list.length === 0 && !targets.isLoading && (
        <p className="muted">{t("targets.noTargets")}</p>
      )}

      {canEdit && tab === "available" && (
        <div className="tgt-connect card">
          <div>
            <h2 className="text-base font-medium">{t("targets.ownTarget")}</h2>
            <p className="muted text-xs mt-1" style={{ maxWidth: 560 }}>
              {t("targets.connectHint")}
            </p>
          </div>
          <div className="flex items-center gap-2 ml-auto">
            <button className="btn sm" onClick={() => setForm("manifest")}>
              {t("targets.uploadManifest")}
            </button>
            <button className="btn sm" onClick={() => setForm("mcp")}>
              {t("targets.addMcp")}
            </button>
          </div>
        </div>
      )}
      {!canEdit && (
        <p className="muted text-xs mt-4">{t("targets.noAccess", { role: me.Role })}</p>
      )}

      {open && (
        <TargetDetail
          plugin={open}
          canEdit={canEdit}
          onClose={() => setDetail(null)}
          onToggle={() => toggle.mutate({ name: open.name, enabled: !open.enabled })}
          togglePending={toggle.isPending}
          onDelete={() => setConfirmDel(open)}
          onDiscover={(token) => discover.mutate({ name: open.name, token })}
          discoverPending={discover.isPending}
        />
      )}
      {confirmDel && (
        <ConfirmDialog
          title={t("targets.delete")}
          confirmLabel={t("targets.delete")}
          pending={remove.isPending}
          onClose={() => setConfirmDel(null)}
          onConfirm={() => remove.mutate(confirmDel.name)}
        >
          <p className="text-sm">{t("targets.deleteConfirm", { name: confirmDel.name })}</p>
        </ConfirmDialog>
      )}
      {form === "manifest" && <ManifestForm onClose={() => setForm(null)} onDone={invalidate} />}
      {form === "mcp" && <AddMCP onClose={() => setForm(null)} onDone={invalidate} />}
    </div>
  );
}

function TargetCard({
  plugin: p,
  canEdit,
  busy,
  onDetails,
  onSetup,
  onToggle,
}: {
  plugin: TargetPlugin;
  canEdit: boolean;
  busy: boolean;
  onDetails: () => void;
  onSetup: () => void;
  onToggle: () => void;
}) {
  const { t } = useTranslation();
  const tools = p.manifest?.tools?.length ?? 0;
  const host = hostOf(p.manifest?.url);
  return (
    <article className={`card tgt-card${p.enabled ? "" : " off"}`}>
      <div className="tgt-head">
        <TargetMark plugin={p} />
        <div className="tgt-id">
          <div className="tgt-name" title={p.label || p.name}>
            {p.label || p.name}
          </div>
          <div className="tgt-slug mono">{p.name}</div>
        </div>
        <span className={`tgt-kind k-${p.kind}`}>{t(kindKey[p.kind])}</span>
      </div>

      <p className="tgt-desc">{p.description || "—"}</p>

      <div className="tgt-meta">
        <span>{t(catLabelKey(p.category && catOrder.includes(p.category) ? p.category : "other"))}</span>
        {host && <span className="mono">{host}</span>}
        {p.kind === "mcp" && <span>{t("targets.toolCount", { count: tools })}</span>}
      </div>

      <div className="tgt-foot">
        <span className={`tgt-state${p.enabled ? " on" : ""}`}>
          <i aria-hidden="true" />
          {p.enabled ? t("targets.active") : t("targets.inactive")}
        </span>
        <button className="btn sm" onClick={onDetails}>
          {t("targets.details")}
        </button>
        {canEdit && (
          <button className={`btn sm${p.enabled ? "" : " cta"}`} onClick={onSetup}>
            {t("targets.wizard.open")}
          </button>
        )}
      </div>
    </article>
  );
}

function TargetDetail({
  plugin: p,
  canEdit,
  onClose,
  onToggle,
  togglePending,
  onDelete,
  onDiscover,
  discoverPending,
}: {
  plugin: TargetPlugin;
  canEdit: boolean;
  onClose: () => void;
  onToggle: () => void;
  togglePending: boolean;
  onDelete: () => void;
  onDiscover: (token?: string) => void;
  discoverPending: boolean;
}) {
  const { t } = useTranslation();
  const [token, setToken] = useState("");
  const tools: MCPTool[] = p.manifest?.tools ?? [];
  const deletable = p.kind === "custom" || p.kind === "mcp";

  return (
    <Modal
      title={p.label || p.name}
      size="lg"
      onClose={onClose}
      footer={
        <>
          {canEdit && deletable && (
            <button className="btn sm danger" style={{ marginRight: "auto" }} onClick={onDelete}>
              {t("targets.delete")}
            </button>
          )}
          <button className="btn sm" onClick={onClose}>
            {t("targets.close")}
          </button>
          {canEdit && (
            <button className="btn sm primary" disabled={togglePending} onClick={onToggle}>
              {p.enabled ? t("targets.deactivate") : t("targets.activate")}
            </button>
          )}
        </>
      }
    >
      <div className="tgt-dl-head">
        <TargetMark plugin={p} lg />
        <div>
          <div className="flex items-center gap-2 flex-wrap">
            <span className="mono text-xs muted">{p.name}</span>
            <span className={`tgt-kind k-${p.kind}`}>{t(kindKey[p.kind])}</span>
            <span className="tgt-kind">
              {t(catLabelKey(p.category && catOrder.includes(p.category) ? p.category : "other"))}
            </span>
            <span className={`tgt-state${p.enabled ? " on" : ""}`}>
              <i aria-hidden="true" />
              {p.enabled ? t("targets.active") : t("targets.inactive")}
            </span>
          </div>
          {p.manifest?.url && (
            <div className="mono text-[11px] muted mt-1" style={{ wordBreak: "break-all" }}>
              {p.manifest.url}
            </div>
          )}
        </div>
      </div>

      <section className="tgt-sec">
        <h3 className="tgt-sec-h">{t("targets.about")}</h3>
        <p className="text-xs secondary" style={{ lineHeight: 1.65 }}>
          {p.description || "—"}
        </p>
      </section>

      {p.setup_doc && (
        <section className="tgt-sec">
          <h3 className="tgt-sec-h">{t("targets.setup")}</h3>
          <pre className="tgt-doc">{p.setup_doc}</pre>
        </section>
      )}

      {p.kind === "mcp" && (
        <section className="tgt-sec">
          <h3 className="tgt-sec-h">{t("targets.toolCount", { count: tools.length })}</h3>
          {tools.length === 0 && <p className="muted text-xs">{t("targets.noTools")}</p>}
          <ul className="tgt-tools">
            {tools.map((tool) => (
              <li key={tool.name}>
                <span className="mono">{tool.name}</span>
                {tool.description && (
                  <span className="muted"> — {tool.description.split("\n")[0]}</span>
                )}
              </li>
            ))}
          </ul>
          {canEdit && (
            <div className="flex items-center gap-2 mt-3">
              <input
                type="password"
                placeholder={t("targets.tokenPlaceholder")}
                className="mono"
                style={{ fontSize: 11, flex: 1, maxWidth: 320 }}
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <button
                className="btn sm"
                disabled={discoverPending}
                onClick={() => onDiscover(token || undefined)}
              >
                {t("targets.updateTools")}
              </button>
            </div>
          )}
        </section>
      )}
    </Modal>
  );
}

function ManifestForm({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const [manifestText, setManifestText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const upload = useMutation({
    mutationFn: (raw: string) => api("/targets", { method: "POST", body: raw }),
    onSuccess: () => {
      onDone();
      onClose();
    },
    onError: (e: Error) => setError(e.message),
  });

  const readFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => setManifestText(String(reader.result ?? ""));
    reader.readAsText(file);
  };

  return (
    <Modal
      title={t("targets.uploadManifestTitle")}
      size="lg"
      onClose={onClose}
      footer={
        <>
          <input
            ref={fileRef}
            type="file"
            accept=".json,application/json"
            style={{ display: "none" }}
            onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])}
          />
          <button className="btn sm" style={{ marginRight: "auto" }} onClick={() => fileRef.current?.click()}>
            {t("targets.chooseFile")}
          </button>
          <button className="btn sm" onClick={() => setManifestText(exampleManifest)}>
            {t("targets.insertExample")}
          </button>
          <button className="btn sm" onClick={onClose}>
            {t("targets.cancel")}
          </button>
          <button
            className="btn sm primary"
            disabled={upload.isPending || !manifestText.trim()}
            onClick={() => {
              setError(null);
              upload.mutate(manifestText);
            }}
          >
            {t("targets.upload")}
          </button>
        </>
      }
    >
      <p className="muted text-xs mb-2">{t("targets.manifestFormDesc")}</p>
      <textarea
        className="code"
        style={{ width: "100%", minHeight: 260 }}
        placeholder={exampleManifest}
        value={manifestText}
        onChange={(e) => setManifestText(e.target.value)}
      />
      {error && (
        <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>
          {error}
        </p>
      )}
    </Modal>
  );
}

function AddMCP({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { t } = useTranslation();
  const [f, setF] = useState({ name: "", label: "", description: "", url: "", header: "", format: "", token: "" });
  const [msg, setMsg] = useState<string | null>(null);
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });

  const create = useMutation({
    mutationFn: () =>
      post<{ name: string; tools?: MCPTool[]; discover_error?: string }>("/targets/mcp", {
        name: f.name.trim(),
        label: f.label.trim(),
        description: f.description.trim(),
        url: f.url.trim(),
        auth: f.header || f.format ? { header: f.header.trim(), format: f.format.trim() } : {},
        token: f.token,
      }),
    onSuccess: (res) => {
      onDone();
      if (res.discover_error) {
        setMsg(t("targets.discoverError", { err: res.discover_error }));
      } else {
        onClose();
      }
    },
    onError: (e: Error) => setMsg(e.message),
  });

  return (
    <Modal
      title={t("targets.mcpSection")}
      onClose={onClose}
      footer={
        <>
          <button className="btn sm" onClick={onClose}>
            {t("targets.cancel")}
          </button>
          <button
            className="btn sm primary"
            disabled={create.isPending || !f.name.trim() || !f.url.trim()}
            onClick={() => {
              setMsg(null);
              create.mutate();
            }}
          >
            {t("targets.mcpConnect")}
          </button>
        </>
      }
    >
      <p className="muted text-xs mb-3">{t("targets.mcpFormDesc")}</p>
      <div className="grid gap-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
        <label className="text-xs">
          {t("targets.mcpNameSlug")}
          <input className="mono" value={f.name} onChange={set("name")} placeholder="weather" />
        </label>
        <label className="text-xs">
          {t("targets.mcpLabel")}
          <input value={f.label} onChange={set("label")} placeholder="Wetterdienst" />
        </label>
      </div>
      <label className="text-xs">
        {t("targets.mcpUrl")}
        <input className="mono" value={f.url} onChange={set("url")} placeholder="https://mcp.example.com/mcp" />
      </label>
      <label className="text-xs">
        {t("targets.mcpDesc")}
        <input value={f.description} onChange={set("description")} />
      </label>
      <div className="grid gap-2 mt-1" style={{ gridTemplateColumns: "1fr 1fr" }}>
        <label className="text-xs">
          {t("targets.mcpAuthHeader")}
          <input className="mono" value={f.header} onChange={set("header")} placeholder="Authorization" />
        </label>
        <label className="text-xs">
          {t("targets.mcpAuthFormat")}
          <input className="mono" value={f.format} onChange={set("format")} placeholder="Bearer {token}" />
        </label>
      </div>
      <label className="text-xs">
        {t("targets.mcpToken")}
        <input type="password" className="mono" value={f.token} onChange={set("token")} />
      </label>
      {msg && (
        <p className="text-xs mt-2" style={{ color: "var(--text-danger)" }}>
          {msg}
        </p>
      )}
    </Modal>
  );
}
