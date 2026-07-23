import { useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, del, patch, post, type MCPTool, type Principal, type TargetPlugin } from "../api";

const kindColor = (k: TargetPlugin["kind"]) =>
  k === "builtin" ? "var(--text-secondary)" : "var(--clay)";

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

export default function Targets({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [manifestText, setManifestText] = useState("");
  const [showUpload, setShowUpload] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);

  const targets = useQuery({
    queryKey: ["targets"],
    queryFn: () => api<TargetPlugin[] | null>("/targets"),
  });

  const canEdit = me.Role === "platform_admin" || me.Role === "security";
  const invalidate = () => qc.invalidateQueries({ queryKey: ["targets"] });

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) =>
      patch(`/targets/${name}`, { enabled }),
    onSuccess: invalidate,
  });

  const remove = useMutation({
    mutationFn: (name: string) => del(`/targets/${name}`),
    onSuccess: invalidate,
  });

  const upload = useMutation({
    mutationFn: (raw: string) =>
      api("/targets", { method: "POST", body: raw }),
    onSuccess: () => {
      setManifestText("");
      setShowUpload(false);
      setError(null);
      invalidate();
    },
    onError: (e: Error) => setError(e.message),
  });

  const discover = useMutation({
    mutationFn: ({ name, token }: { name: string; token?: string }) =>
      post(`/targets/${name}/discover`, token ? { token } : {}),
    onSuccess: invalidate,
  });

  const readFile = (file: File) => {
    const reader = new FileReader();
    reader.onload = () => setManifestText(String(reader.result ?? ""));
    reader.readAsText(file);
  };

  const list = targets.data ?? [];

  return (
    <div>
      <div className="flex items-baseline gap-3 mb-2">
        <h1 className="text-[22px]">{t("targets.title")}</h1>
        <span className="muted">{t("targets.subtitle")}</span>
      </div>
      <p className="muted text-xs mb-5" style={{ maxWidth: 640 }}>
        {t("targets.desc")}
      </p>

      <div className="grid gap-3 mb-6" style={{ gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))" }}>
        {list.map((p) => (
          <div key={p.name} className="card" style={{ padding: "14px 16px", opacity: p.enabled ? 1 : 0.6 }}>
            <div className="flex items-center gap-2 mb-1">
              <span className="font-medium">{p.label || p.name}</span>
              <span className="mono text-xs muted">{p.name}</span>
              <span className="text-[11px] ml-auto" style={{ color: kindColor(p.kind) }}>
                {t(p.kind === "custom" ? "targets.kindCustom" : p.kind === "mcp" ? "targets.kindMcp" : "targets.kindBuiltin")}
              </span>
            </div>
            <p className="muted text-xs mb-3">{p.description || "—"}</p>
            {p.setup_doc && <SetupInfo doc={p.setup_doc} />}
            {p.kind === "mcp" && (
              <MCPTools plugin={p} canEdit={canEdit} onDiscover={(token) => discover.mutate({ name: p.name, token })} pending={discover.isPending} />
            )}
            <div className="flex items-center gap-2">
              <span className="text-[11px]" style={{ color: p.enabled ? "var(--text-secondary)" : "var(--clay)" }}>
                {p.enabled ? t("targets.active") : t("targets.inactive")}
              </span>
              {canEdit && (
                <button
                  className="btn sm ml-auto"
                  disabled={toggle.isPending}
                  onClick={() => toggle.mutate({ name: p.name, enabled: !p.enabled })}
                >
                  {p.enabled ? t("targets.deactivate") : t("targets.activate")}
                </button>
              )}
              {canEdit && (p.kind === "custom" || p.kind === "mcp") && (
                <button
                  className="btn sm danger"
                  disabled={remove.isPending}
                  onClick={() => {
                    if (confirm(t("targets.deleteConfirm", { name: p.name }))) remove.mutate(p.name);
                  }}
                >
                  {t("targets.delete")}
                </button>
              )}
            </div>
          </div>
        ))}
        {list.length === 0 && !targets.isLoading && (
          <p className="muted">{t("targets.noTargets")}</p>
        )}
      </div>

      {canEdit && (
        <>
          <h2 className="text-base font-medium mb-2">{t("targets.ownTarget")}</h2>
          {!showUpload ? (
            <button className="btn" onClick={() => setShowUpload(true)}>
              {t("targets.uploadManifest")}
            </button>
          ) : (
            <div className="card" style={{ padding: "14px 16px", maxWidth: 720 }}>
              <p className="muted text-xs mb-2">
                {t("targets.manifestFormDesc")}
              </p>
              <textarea
                className="mono"
                style={{ width: "100%", minHeight: 220, fontSize: 12 }}
                placeholder={exampleManifest}
                value={manifestText}
                onChange={(e) => setManifestText(e.target.value)}
              />
              {error && (
                <p className="text-xs mb-2" style={{ color: "var(--clay)" }}>
                  {error}
                </p>
              )}
              <div className="flex items-center gap-2 mt-2">
                <input
                  ref={fileRef}
                  type="file"
                  accept=".json,application/json"
                  style={{ display: "none" }}
                  onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])}
                />
                <button className="btn sm" onClick={() => fileRef.current?.click()}>
                  {t("targets.chooseFile")}
                </button>
                <button className="btn sm" onClick={() => setManifestText(exampleManifest)}>
                  {t("targets.insertExample")}
                </button>
                <button
                  className="btn sm primary ml-auto"
                  disabled={upload.isPending || !manifestText.trim()}
                  onClick={() => upload.mutate(manifestText)}
                >
                  {t("targets.upload")}
                </button>
                <button
                  className="btn sm"
                  onClick={() => {
                    setShowUpload(false);
                    setError(null);
                  }}
                >
                  {t("targets.cancel")}
                </button>
              </div>
            </div>
          )}
          <AddMCP onDone={invalidate} />
        </>
      )}
      {!canEdit && (
        <p className="muted text-xs mt-3">
          {t("targets.noAccess", { role: me.Role })}
        </p>
      )}
    </div>
  );
}

function SetupInfo({ doc }: { doc: string }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  return (
    <div className="mb-3">
      <button className="btn sm" onClick={() => setOpen((o) => !o)}>
        ⓘ {t("targets.setup")} {open ? "▲" : "▼"}
      </button>
      {open && (
        <pre
          className="text-[11px] mt-2"
          style={{
            whiteSpace: "pre-wrap",
            wordBreak: "break-word",
            background: "var(--surface-1)",
            border: "1px solid var(--border)",
            borderRadius: "var(--radius)",
            padding: "10px 12px",
            margin: 0,
            fontFamily: "inherit",
            lineHeight: 1.5,
          }}
        >
          {doc}
        </pre>
      )}
    </div>
  );
}

function MCPTools({
  plugin,
  canEdit,
  onDiscover,
  pending,
}: {
  plugin: TargetPlugin;
  canEdit: boolean;
  onDiscover: (token?: string) => void;
  pending: boolean;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [token, setToken] = useState("");
  const tools: MCPTool[] = plugin.manifest?.tools ?? [];
  const url = plugin.manifest?.url;
  return (
    <div className="mb-3">
      {url && <p className="mono text-[11px] muted mb-1" style={{ wordBreak: "break-all" }}>{url}</p>}
      <button className="btn sm mb-2" onClick={() => setOpen((o) => !o)}>
        {tools.length} Tool{tools.length === 1 ? "" : "s"} {open ? "▲" : "▼"}
      </button>
      {open && (
        <div className="mb-2">
          {tools.length === 0 && <p className="muted text-xs">{t("targets.noTools")}</p>}
          <ul className="text-xs" style={{ listStyle: "none", padding: 0, margin: 0 }}>
            {tools.map((tool) => (
              <li key={tool.name} className="mb-1">
                <span className="mono">{tool.name}</span>
                {tool.description && <span className="muted"> — {tool.description.split("\n")[0]}</span>}
              </li>
            ))}
          </ul>
          {canEdit && (
            <div className="flex items-center gap-2 mt-2">
              <input
                type="password"
                placeholder={t("targets.tokenPlaceholder")}
                className="mono"
                style={{ fontSize: 11, flex: 1 }}
                value={token}
                onChange={(e) => setToken(e.target.value)}
              />
              <button className="btn sm" disabled={pending} onClick={() => onDiscover(token || undefined)}>
                {t("targets.updateTools")}
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function AddMCP({ onDone }: { onDone: () => void }) {
  const { t } = useTranslation();
  const [show, setShow] = useState(false);
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
        setMsg(t("targets.discoverSuccess", { name: res.name, count: res.tools?.length ?? 0 }));
        setF({ name: "", label: "", description: "", url: "", header: "", format: "", token: "" });
        setShow(false);
      }
    },
    onError: (e: Error) => setMsg(e.message),
  });

  return (
    <div className="mt-6">
      <h2 className="text-base font-medium mb-2">{t("targets.mcpSection")}</h2>
      {!show ? (
        <button className="btn" onClick={() => setShow(true)}>
          {t("targets.addMcp")}
        </button>
      ) : (
        <div className="card" style={{ padding: "14px 16px", maxWidth: 720 }}>
          <p className="muted text-xs mb-3">
            {t("targets.mcpFormDesc")}
          </p>
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
            <p className="text-xs mt-2" style={{ color: "var(--clay)" }}>
              {msg}
            </p>
          )}
          <div className="flex items-center gap-2 mt-3">
            <button
              className="btn sm primary ml-auto"
              disabled={create.isPending || !f.name.trim() || !f.url.trim()}
              onClick={() => {
                setMsg(null);
                create.mutate();
              }}
            >
              {t("targets.mcpConnect")}
            </button>
            <button className="btn sm" onClick={() => { setShow(false); setMsg(null); }}>
              {t("targets.cancel")}
            </button>
          </div>
        </div>
      )}
      {!show && msg && (
        <p className="text-xs mt-2 muted">{msg}</p>
      )}
    </div>
  );
}
