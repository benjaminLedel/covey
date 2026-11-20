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
import { Link } from "react-router";
import { Markdown } from "../../components/Markdown";
import { NavIcon } from "../../components/navicons";
import Section from "../../components/Section";

/* The files a team lead writes, in reading order, under plain names (#745).
   The file name stays as a small label: it is what the agent, the bundle and
   the docs call it. */
const DOCS = [
  { file: "SOUL.md", key: "soul" },
  { file: "PLAYBOOKS.md", key: "playbooks" },
  { file: "CAPABILITIES.md", key: "capabilities" },
  { file: "ORG.md", key: "org" },
  { file: "HEARTBEAT.md", key: "heartbeat", code: true },
] as const;
const DOC_FILES: string[] = DOCS.map((d) => d.file);

/* Files with a home elsewhere: ACCESS.md and EGRESS.md are a text view onto
   the tools and egress stores, KPIS.md feeds the work record. They stay
   reachable here for whoever wants the text, folded away. */
const ELSEWHERE: Record<string, { key: string; link?: string }> = {
  "ACCESS.md": { key: "access", link: "?tab=werkzeuge" },
  "EGRESS.md": { key: "egress", link: "?tab=einstellungen&sub=egress" },
  "KPIS.md": { key: "kpis" },
  "TONE.md": { key: "tone" },
};

/* The heading a file opens with ("# Playbooks") repeats the section title
   right above it; reading view leaves it out, the file keeps it. */
function withoutTitle(text: string): string {
  return text.replace(/^\s*#\s[^\n]*\n+/, "");
}

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
  const saved = cfg.data?.files ?? {};
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const [editing, setEditing] = useState<Set<string>>(new Set());
  const files = draft ?? saved;
  const changed = Object.keys(files).filter((f) => (files[f] ?? "") !== (saved[f] ?? ""));
  const setFile = (name: string, content: string) => setDraft({ ...files, [name]: content });
  const edit = (name: string, on: boolean) =>
    setEditing((prev) => {
      const n = new Set(prev);
      if (on) n.add(name);
      else n.delete(name);
      return n;
    });

  const invalidate = () => {
    qc.invalidateQueries({ queryKey: ["config", agentId] });
    qc.invalidateQueries({ queryKey: ["agent-tools", agentId] });
    qc.invalidateQueries({ queryKey: ["egress", "agent", agentId] });
    qc.invalidateQueries({ queryKey: ["heartbeats", agentId] });
  };
  const save = useMutation({
    mutationFn: () => put(`/agents/${agentId}/config`, { files }),
    onSuccess: () => {
      setDraft(null);
      setEditing(new Set());
      invalidate();
    },
  });
  const discard = () => {
    setDraft(null);
    setEditing(new Set());
    save.reset();
  };

  const version = cfg.data?.version ?? 0;
  const label = (f: string) => {
    const d = DOCS.find((x) => x.file === f);
    return d ? t(`agent.config.doc.${d.key}.title`) : f;
  };
  const others = Object.keys(files)
    .filter((f) => !DOC_FILES.includes(f))
    .sort((x, y) => x.localeCompare(y));

  if (cfg.isLoading) return null;

  return (
    <div className="cfg">
      <p className="ps-desc mt-0">
        {t("agent.config.intro")}
        {version > 0 && <> {t("agent.config.versionNow", { v: version })}</>}
      </p>

      {canManage && (
        <ConfigAssistant agentId={agentId} files={files} onApply={(file, content) => setFile(file, content)} />
      )}

      <div className="cfg-docs">
        {DOCS.map((d) => {
          const content = files[d.file] ?? "";
          const isEditing = editing.has(d.file);
          const empty = content.trim() === "";
          return (
            <section key={d.file} className="cfg-doc">
              <div className="cfg-doc-head">
                <div className="min-w-0">
                  <h3 className="cfg-doc-title">
                    {t(`agent.config.doc.${d.key}.title`)}
                    <span className="cfg-file">{d.file}</span>
                    {changed.includes(d.file) && <span className="cfg-dirty">{t("agent.config.changed")}</span>}
                  </h3>
                  <p className="ps-hint m-0">{t(`agent.config.doc.${d.key}.desc`)}</p>
                </div>
                {canManage && !isEditing && (
                  <button className="btn sm" onClick={() => edit(d.file, true)}>
                    <NavIcon name={empty ? "plus" : "edit"} />
                    {empty ? t("agent.config.add") : t("agent.config.edit")}
                  </button>
                )}
                {isEditing && (
                  <button className="btn sm" onClick={() => edit(d.file, false)}>
                    <NavIcon name="check" />
                    {t("agent.config.done")}
                  </button>
                )}
              </div>
              {isEditing ? (
                <textarea
                  className={`cfg-editor${"code" in d ? " code" : ""}`}
                  aria-label={t(`agent.config.doc.${d.key}.title`)}
                  rows={Math.min(24, Math.max(6, content.split("\n").length + 2))}
                  value={content}
                  placeholder={t(`agent.config.doc.${d.key}.placeholder`)}
                  onChange={(e) => setFile(d.file, e.target.value)}
                  autoFocus
                />
              ) : empty ? null : "code" in d ? (
                <pre className="cfg-code">{content}</pre>
              ) : (
                <div className="cfg-read">
                  <Markdown text={withoutTitle(content)} />
                </div>
              )}
            </section>
          );
        })}
      </div>

      {others.length > 0 && (
        <details className="ps-advanced cfg-more">
          <summary>
            <span className="ps-title">{t("agent.config.more")}</span>
            <span className="ps-hint">{t("agent.config.moreDesc", { files: others.join(", ") })}</span>
          </summary>
          {others.map((f) => {
            const where = ELSEWHERE[f];
            return (
              <div key={f} className="cfg-other">
                <div className="cfg-doc-head">
                  <div className="min-w-0">
                    <h3 className="cfg-doc-title">
                      <span className="mono">{f}</span>
                      {changed.includes(f) && <span className="cfg-dirty">{t("agent.config.changed")}</span>}
                    </h3>
                    <p className="ps-hint m-0">
                      {t(`agent.config.where.${where?.key ?? "other"}`)}
                      {where?.link && (
                        <>
                          {" "}
                          <Link to={where.link}>{t("agent.config.openThere")}</Link>
                        </>
                      )}
                    </p>
                  </div>
                </div>
                <textarea
                  className="cfg-editor code"
                  aria-label={f}
                  rows={Math.min(14, Math.max(4, (files[f] ?? "").split("\n").length + 1))}
                  value={files[f] ?? ""}
                  readOnly={!canManage}
                  onChange={(e) => setFile(f, e.target.value)}
                />
              </div>
            );
          })}
        </details>
      )}

      {canExport && (
        <ShareSection agentId={agentId} slug={slug} displayName={displayName} canManage={canManage} onImported={() => { discard(); invalidate(); }} />
      )}

      {changed.length > 0 && (
        <div className="cfg-savebar" role="region" aria-label={t("agent.config.unsavedTitle")}>
          <span className="min-w-0">
            <span className="ps-strong">{t("agent.config.unsavedTitle")}</span>
            <span className="ps-hint">{changed.map(label).join(", ")}</span>
          </span>
          {save.isError && <span className="danger-text text-xs">{(save.error as Error).message}</span>}
          <button className="btn" onClick={discard} disabled={save.isPending}>
            {t("agent.config.discard")}
          </button>
          <button className="btn primary" onClick={() => save.mutate()} disabled={save.isPending}>
            <NavIcon name="check" />
            {t("agent.config.saveVersion", { v: version + 1 })}
          </button>
        </div>
      )}
    </div>
  );
}

/* Template and bundle: passing the configuration on rather than writing it.
   Below the content, because it is the rarer thing to do here. */
function ShareSection({
  agentId,
  slug,
  displayName,
  canManage,
  onImported,
}: {
  agentId: string;
  slug: string;
  displayName: string;
  canManage: boolean;
  onImported: () => void;
}) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [step, setStep] = useState<"" | "template" | "import">("");
  const [templateName, setTemplateName] = useState("");
  const [templateDesc, setTemplateDesc] = useState("");
  const [bundle, setBundle] = useState<unknown>(null);
  const [importError, setImportError] = useState("");
  const importRef = useRef<HTMLInputElement>(null);

  const saveTemplate = useMutation({
    mutationFn: () =>
      post("/templates", { name: templateName.trim(), description: templateDesc.trim(), from_agent_id: agentId }),
    onSuccess: () => {
      setStep("");
      setTemplateDesc("");
      qc.invalidateQueries({ queryKey: ["templates"] });
    },
  });
  // Bundle import: overwrites ONLY the config of this agent (master data,
  // secrets, guard rails and the rest stay untouched).
  const importCfg = useMutation({
    mutationFn: () => post<ConfigVersion>(`/agents/${agentId}/config/import`, bundle),
    onSuccess: () => {
      setStep("");
      setBundle(null);
      onImported();
    },
    onError: (e) => setImportError(String((e as Error)?.message)),
  });
  const pickBundle = async (f: File | undefined) => {
    if (!f) return;
    setImportError("");
    try {
      setBundle(JSON.parse(await f.text()));
      setStep("import");
    } catch {
      setImportError(t("agent.config.importInvalidJson"));
    }
  };

  return (
    <Section title={t("agent.config.share")} desc={t("agent.config.shareDesc")}>
      <div className="flex gap-2 flex-wrap">
        {canManage && (
          <button
            className="btn sm"
            onClick={() => {
              setTemplateName(displayName);
              setStep("template");
            }}
          >
            <NavIcon name="copy" />
            {t("agent.config.saveTemplate")}
          </button>
        )}
        <a className="btn sm no-underline" href={`/api/v1/agents/${agentId}/export`} download={`${slug}-config.json`}>
          <NavIcon name="arrowDown" />
          {t("agent.config.exportBundle")}
        </a>
        {canManage && (
          <>
            <button className="btn sm" type="button" onClick={() => importRef.current?.click()}>
              <NavIcon name="arrowUp" />
              {t("agent.config.importBundle")}
            </button>
            <input
              ref={importRef}
              type="file"
              accept="application/json,.json"
              hidden
              onChange={(e) => {
                void pickBundle(e.target.files?.[0]);
                e.target.value = "";
              }}
            />
          </>
        )}
      </div>
      {step === "template" && (
        <form
          className="edit-panel mt-3"
          onSubmit={(e) => {
            e.preventDefault();
            saveTemplate.mutate();
          }}
        >
          <div className="field-grid two">
            <div>
              <label htmlFor="tpl-name">{t("agent.config.templateName")}</label>
              <input id="tpl-name" autoFocus value={templateName} onChange={(e) => setTemplateName(e.target.value)} required />
            </div>
            <div>
              <label htmlFor="tpl-desc">{t("agent.config.templateDesc")}</label>
              <input id="tpl-desc" value={templateDesc} onChange={(e) => setTemplateDesc(e.target.value)} />
            </div>
          </div>
          <div className="edit-panel-foot">
            {saveTemplate.isError && (
              <span className="danger-text text-xs mr-auto">{String((saveTemplate.error as Error)?.message)}</span>
            )}
            <button className="btn" type="button" onClick={() => setStep("")}>
              {t("agent.config.cancel")}
            </button>
            <button className="btn primary" disabled={saveTemplate.isPending || !templateName.trim()}>
              <NavIcon name="check" />
              {t("agent.config.saveTemplate")}
            </button>
          </div>
        </form>
      )}
      {step === "import" && (
        <div className="edit-panel danger mt-3" role="alertdialog" aria-label={t("agent.config.importBundle")}>
          <span className="ps-strong">{t("agent.config.importConfirm")}</span>
          <div className="edit-panel-foot">
            <button className="btn" onClick={() => { setStep(""); setBundle(null); }} autoFocus>
              {t("agent.config.cancel")}
            </button>
            <button className="btn danger solid" onClick={() => importCfg.mutate()} disabled={importCfg.isPending}>
              {t("agent.config.importOverwrite")}
            </button>
          </div>
        </div>
      )}
      {importError && <p className="danger-text text-xs mt-2 mb-0">{importError}</p>}
    </Section>
  );
}

// ConfigAssistant is the AI assistant for adjusting agents (FR-001). It
// appears only when a Claude credential is on file org-wide. Its proposals are
// taken into the config draft (onApply) — they take effect only through
// deliberately saving a new version.
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

  if (!status.data?.available) return null; // Gating: no Claude credential → no UI.

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
