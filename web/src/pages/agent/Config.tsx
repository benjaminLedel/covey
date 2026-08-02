import { useState, useRef } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import {
  api,
  post,
  put,
  assistStatus,
  configAssist,
  type AssistMessage,
  type AssistProposal,
  type ConfigVersion,
} from "../../api";
import { Markdown } from "../../components/Markdown";

export function Config({
  agentId,
  slug,
  displayName,
  canManage,
  canExport,
}: {
  agentId: string;
  slug: string;
  displayName: string;
  canManage: boolean;
  canExport: boolean;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const cfg = useQuery({
    queryKey: ["config", agentId],
    queryFn: () => api<ConfigVersion>(`/agents/${agentId}/config`),
    retry: false,
  });
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const [savingTemplate, setSavingTemplate] = useState(false);
  const [templateName, setTemplateName] = useState("");
  const [templateDesc, setTemplateDesc] = useState("");
  const files = { "SOUL.md": "", "HEARTBEAT.md": "", ...(draft ?? cfg.data?.files ?? { "ACCESS.md": "" }) };

  const save = useMutation({
    mutationFn: () => put(`/agents/${agentId}/config`, { files }),
    onSuccess: () => {
      setDraft(null);
      qc.invalidateQueries({ queryKey: ["config", agentId] });
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
      qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
    },
  });

  const saveTemplate = useMutation({
    mutationFn: () =>
      post("/templates", { name: templateName.trim(), description: templateDesc.trim(), from_agent_id: agentId }),
    onSuccess: () => {
      setSavingTemplate(false);
      setTemplateName("");
      setTemplateDesc("");
      qc.invalidateQueries({ queryKey: ["templates"] });
    },
  });

  // Bundle-Import: überschreibt NUR die Config dieses Agenten aus einer
  // Bundle-JSON (Stammdaten, Secrets, Guard-Rails etc. bleiben unangetastet).
  const importRef = useRef<HTMLInputElement>(null);
  const [importError, setImportError] = useState("");
  const importCfg = useMutation({
    mutationFn: (bundle: unknown) => post<ConfigVersion>(`/agents/${agentId}/config/import`, bundle),
    onSuccess: () => {
      setDraft(null);
      setImportError("");
      qc.invalidateQueries({ queryKey: ["config", agentId] });
      qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
      qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
      qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
    },
    onError: (e) => setImportError(String((e as Error)?.message)),
  });
  const pickBundle = async (f: File | undefined) => {
    if (!f) return;
    setImportError("");
    if (!window.confirm(t("agent.config.importConfirm"))) return;
    try {
      importCfg.mutate(JSON.parse(await f.text()));
    } catch {
      setImportError(t("agent.config.importInvalidJson"));
    }
  };

  return (
    <div>
      {canExport && (
        <div className="flex gap-2 mb-2 justify-end">
          {canManage && !savingTemplate && (
            <button
              className="btn sm"
              onClick={() => { setTemplateName(displayName); setSavingTemplate(true); }}
              title="Aktuelle Konfiguration als wiederverwendbare Vorlage speichern"
            >
              {t("agent.config.saveTemplate")}
            </button>
          )}
          {savingTemplate && (
            <form
              className="flex gap-2 items-center"
              onSubmit={(e) => { e.preventDefault(); saveTemplate.mutate(); }}
            >
              <input
                autoFocus
                placeholder={t("agent.config.templateName")}
                value={templateName}
                onChange={(e) => setTemplateName(e.target.value)}
                style={{ width: 180 }}
                required
              />
              <input
                placeholder={t("agent.config.templateDesc")}
                value={templateDesc}
                onChange={(e) => setTemplateDesc(e.target.value)}
                style={{ width: 180 }}
              />
              <button className="btn sm primary" disabled={saveTemplate.isPending} type="submit">
                {saveTemplate.isPending ? "…" : t("agent.config.saveBtn")}
              </button>
              <button className="btn sm" type="button" onClick={() => setSavingTemplate(false)}>
                {t("agent.config.cancel")}
              </button>
              {saveTemplate.isError && (
                <span className="danger-text text-xs">{String((saveTemplate.error as Error)?.message)}</span>
              )}
            </form>
          )}
          <a
            className="btn sm no-underline"
            href={`/api/v1/agents/${agentId}/export`}
            download={`${slug}-config.json`}
            title="Komplette Konfiguration (inkl. Stages, Guard-Rails, Egress, Secret-Namen) als JSON-Bundle herunterladen — Import auf der Agenten-Übersicht"
          >
            {t("agent.config.exportBundle")}
          </a>
          {canManage && (
            <>
              <button
                className="btn sm"
                type="button"
                disabled={importCfg.isPending}
                onClick={() => importRef.current?.click()}
                title="Ein JSON-Bundle einlesen und NUR dessen Config-Dateien in diesen Agenten übernehmen (überschreibt SOUL/HEARTBEAT/ACCESS/EGRESS/… als neue Version). Stammdaten, Secrets und Guard-Rails bleiben unangetastet."
              >
                {importCfg.isPending ? "…" : t("agent.config.importBundle")}
              </button>
              <input
                ref={importRef}
                type="file"
                accept="application/json,.json"
                style={{ display: "none" }}
                onChange={(e) => { pickBundle(e.target.files?.[0]); e.target.value = ""; }}
              />
            </>
          )}
        </div>
      )}
      {importError && <p className="danger-text text-xs mb-2" style={{ textAlign: "right" }}>{importError}</p>}
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.config.versionInfo", {
          version: cfg.data && cfg.data.version > 0
            ? t("agent.config.currentVersion", { v: cfg.data.version })
            : "",
        })}
      </p>
      {canManage && (
        <ConfigAssistant
          agentId={agentId}
          files={files}
          onApply={(file, content) => setDraft({ ...files, [file]: content })}
        />
      )}
      {Object.entries(files)
        .sort(([x], [y]) => x.localeCompare(y))
        .map(([name, content]) => (
          <div key={name} className="mb-3">
            <label className="mono">{name}</label>
            <textarea
              className="code"
              rows={Math.min(14, Math.max(4, content.split("\n").length + 1))}
              value={content}
              readOnly={!canManage}
              onChange={(e) => setDraft({ ...files, [name]: e.target.value })}
            />
          </div>
        ))}
      {canManage && (
        <div className="flex items-center gap-3">
          <button className="btn primary sm" disabled={!draft || save.isPending} onClick={() => save.mutate()}>
            {t("agent.config.newVersion")}
          </button>
          {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
        </div>
      )}
    </div>
  );
}

// ConfigAssistant ist der KI-Assistent zum Anpassen von Agenten (FR-001).
// Er erscheint nur, wenn org-weit ein Claude-Credential hinterlegt ist. Seine
// Vorschläge werden in den Config-Draft übernommen (onApply) — wirksam werden
// sie erst durch bewusstes Speichern einer neuen Version.
type AssistTurn = { role: "user" | "assistant"; content: string; proposals?: AssistProposal[] };

function ConfigAssistant({
  agentId,
  files,
  onApply,
}: {
  agentId: string;
  files: Record<string, string>;
  onApply: (file: string, content: string) => void;
}) {
  const { t } = useTranslation();
  const status = useQuery({ queryKey: ["assist-status"], queryFn: assistStatus, retry: false });
  const [open, setOpen] = useState(false);
  const [input, setInput] = useState("");
  const [turns, setTurns] = useState<AssistTurn[]>([]);
  const [applied, setApplied] = useState<Set<string>>(new Set());

  const ask = useMutation({
    mutationFn: (history: AssistTurn[]) => {
      const msgs: AssistMessage[] = history.map((m) => ({ role: m.role, content: m.content }));
      return configAssist(agentId, msgs, files);
    },
    onSuccess: (res) =>
      setTurns((prev) => [...prev, { role: "assistant", content: res.reply, proposals: res.proposals }]),
  });

  if (!status.data?.available) return null; // Gating: kein Claude-Credential → keine UI.

  const send = () => {
    const text = input.trim();
    if (!text || ask.isPending) return;
    const next: AssistTurn[] = [...turns, { role: "user", content: text }];
    setTurns(next);
    setInput("");
    ask.mutate(next);
  };

  return (
    <div className="assist">
      <div className="assist-head">
        <button
          className="assist-toggle"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          <span className="caret">▶</span>
          {t("agent.config.assist.title")}
        </button>
        {open && turns.length > 0 && (
          <button className="btn sm" onClick={() => { setTurns([]); setApplied(new Set()); }}>
            {t("agent.config.assist.reset")}
          </button>
        )}
      </div>
      {open && (
        <div className="assist-body">
          <p className="muted text-xs mb-3" style={{ maxWidth: 620 }}>
            {t("agent.config.assist.hint")}
          </p>
          {turns.length > 0 && (
            <div className="assist-log mb-3">
              {turns.map((m, i) => (
                <div key={i} className={`assist-msg ${m.role === "user" ? "user" : "bot"}`}>
                  {m.role === "user" ? m.content : <Markdown text={m.content} />}
                  {m.proposals && m.proposals.length > 0 && (
                    <div className="assist-proposals">
                      {m.proposals.map((p) => {
                        const key = `${i}:${p.file}`;
                        const done = applied.has(key);
                        return (
                          <button
                            key={p.file}
                            className="btn sm primary"
                            disabled={done}
                            onClick={() => { onApply(p.file, p.content); setApplied((s) => new Set(s).add(key)); }}
                          >
                            {done
                              ? `${p.file} · ${t("agent.config.assist.applied")}`
                              : t("agent.config.assist.apply", { file: p.file })}
                          </button>
                        );
                      })}
                    </div>
                  )}
                </div>
              ))}
              {ask.isPending && <div className="assist-status">{t("agent.config.assist.thinking")}</div>}
              {ask.isError && (
                <div className="assist-status danger-text">{(ask.error as Error).message}</div>
              )}
            </div>
          )}
          <div className="assist-input">
            <textarea
              rows={2}
              placeholder={t("agent.config.assist.placeholder")}
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); send(); } }}
            />
            <button className="btn primary sm" disabled={!input.trim() || ask.isPending} onClick={send}>
              {t("agent.config.assist.send")}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
