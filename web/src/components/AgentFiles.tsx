import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useSearchParams } from "react-router";
import { useTranslation } from "react-i18next";
import {
  api,
  del,
  post,
  put,
  upload,
  type Agent,
  type FileContent,
  type FileEntry,
  type FileListing,
} from "../api";
import { fmtBytes } from "../format";
import { Modal, ConfirmDialog } from "./Modal";

// Der Arbeitsplatz des Agenten: sein persistentes Home als Dateibaum. Es ist
// die Antwort auf „was liegt bei dem eigentlich rum?" — bisher nur über eine
// Shell auf dem Host zu bekommen. Lesen, hochladen, ändern, löschen; das Home
// überlebt die Sandbox, der Browser funktioniert deshalb auch am schlafenden
// Agenten.
//
// Der aktuelle Ordner und die geöffnete Datei stehen in der URL (?dir=&file=),
// wie überall in dieser Ansicht: ein Link auf eine Datei im Home eines Agenten
// ist etwas, das man verschickt.

const q = (s: string) => encodeURIComponent(s);

export function AgentFiles({ agent, canWrite }: { agent: Agent; canWrite: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [sp, setSp] = useSearchParams();
  const dir = sp.get("dir") ?? "";
  const openFile = sp.get("file");

  const setParam = (key: "dir" | "file", value: string | null) =>
    setSp(
      (prev) => {
        const n = new URLSearchParams(prev);
        if (value === null || value === "") n.delete(key);
        else n.set(key, value);
        if (key === "dir") n.delete("file"); // Ordnerwechsel schließt die Datei
        return n;
      },
      { replace: false },
    );

  const listing = useQuery({
    queryKey: ["agent-files", agent.id, dir],
    queryFn: () => api<FileListing>(`/agents/${agent.id}/files?path=${q(dir)}`),
    retry: false,
  });
  const inval = () => qc.invalidateQueries({ queryKey: ["agent-files", agent.id] });

  const [dropping, setDropping] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [prompting, setPrompting] = useState<null | { kind: "mkdir" | "newfile" | "rename"; entry?: FileEntry }>(null);
  const [confirming, setConfirming] = useState<FileEntry | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      inval();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const uploadFiles = (files: FileList | File[]) => {
    const list = Array.from(files);
    if (list.length === 0) return;
    const form = new FormData();
    for (const f of list) form.append("file", f, f.name);
    return run(() => upload(`/agents/${agent.id}/files/upload?path=${q(dir)}`, form));
  };

  const remove = useMutation({
    mutationFn: (e: FileEntry) => del(`/agents/${agent.id}/files?path=${q(e.path)}`),
    onSuccess: (_d, e) => {
      setConfirming(null);
      if (openFile === e.path) setParam("file", null);
      inval();
    },
    onError: (e) => setError((e as Error).message),
  });

  const crumbs = dir === "" ? [] : dir.split("/");
  const busyNow = busy || remove.isPending;
  const working = ["working", "triage", "triggered"].includes(agent.status);

  return (
    <div>
      <p className="muted text-xs mb-3" style={{ maxWidth: 680 }}>
        {t("agent.files.desc")}
      </p>

      {working && (
        <p className="text-xs mb-3" style={{ color: "var(--text-warning)" }}>
          {t("agent.files.runningHint")}
        </p>
      )}
      {dir.split("/")[0] === "wiki" && (
        <p className="muted text-xs mb-3">{t("agent.files.wikiHint")}</p>
      )}

      {/* Pfadleiste und Werkzeuge in einer Zeile: der Ort, an dem man ist, und
          was man dort tun kann, gehören zusammen. */}
      <div className="card mb-3 flex items-center gap-2 flex-wrap" style={{ padding: "9px 14px" }}>
        <nav className="flex items-center gap-1 text-sm min-w-0" aria-label={t("agent.files.breadcrumb")}>
          <button className="btn sm" style={{ border: "none" }} onClick={() => setParam("dir", "")}>
            {t("agent.files.home")}
          </button>
          {crumbs.map((name, i) => (
            <span key={i} className="flex items-center gap-1 min-w-0">
              <span className="muted">/</span>
              <button
                className="btn sm mono"
                style={{ border: "none", overflow: "hidden", textOverflow: "ellipsis" }}
                onClick={() => setParam("dir", crumbs.slice(0, i + 1).join("/"))}
              >
                {name}
              </button>
            </span>
          ))}
        </nav>
        <span className="ml-auto" />
        {canWrite && (
          <>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                if (e.target.files) uploadFiles(e.target.files);
                e.target.value = "";
              }}
            />
            <button className="btn sm primary" disabled={busyNow} onClick={() => fileInput.current?.click()}>
              {t("agent.files.upload")}
            </button>
            <button className="btn sm" disabled={busyNow} onClick={() => setPrompting({ kind: "newfile" })}>
              {t("agent.files.newFile")}
            </button>
            <button className="btn sm" disabled={busyNow} onClick={() => setPrompting({ kind: "mkdir" })}>
              {t("agent.files.newFolder")}
            </button>
          </>
        )}
        <button className="btn sm" disabled={listing.isFetching} onClick={() => inval()}>
          {t("agent.files.refresh")}
        </button>
      </div>

      {error && <p className="danger-text text-xs mb-2">{error}</p>}

      <div
        className="card"
        style={{
          padding: 0,
          outline: dropping ? "2px dashed var(--text-accent)" : "none",
          outlineOffset: -4,
        }}
        onDragOver={(e) => {
          if (!canWrite) return;
          e.preventDefault();
          setDropping(true);
        }}
        onDragLeave={() => setDropping(false)}
        onDrop={(e) => {
          if (!canWrite) return;
          e.preventDefault();
          setDropping(false);
          if (e.dataTransfer.files.length) uploadFiles(e.dataTransfer.files);
        }}
      >
        {listing.isError && (
          <p className="danger-text text-xs" style={{ padding: "14px" }}>
            {(listing.error as Error).message}
          </p>
        )}
        {listing.data && listing.data.entries.length === 0 && (
          <p className="muted text-sm" style={{ padding: "18px 14px" }}>
            {listing.data.exists || dir !== ""
              ? t("agent.files.emptyDir")
              : t("agent.files.noHome")}
          </p>
        )}
        {listing.data && listing.data.entries.length > 0 && (
          <table className="tbl">
            <thead>
              <tr>
                <th>{t("agent.files.colName")}</th>
                <th style={{ width: 100 }}>{t("agent.files.colSize")}</th>
                <th style={{ width: 160 }}>{t("agent.files.colModified")}</th>
                <th style={{ width: 260 }} />
              </tr>
            </thead>
            <tbody>
              {listing.data.entries.map((e) => (
                <tr key={e.path}>
                  <td style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    <button
                      className="btn sm mono"
                      style={{ border: "none", padding: "2px 4px", maxWidth: "100%" }}
                      disabled={e.outside}
                      title={e.symlink ? t("agent.files.symlinkTo", { target: e.symlink }) : e.name}
                      onClick={() => (e.is_dir ? setParam("dir", e.path) : setParam("file", e.path))}
                    >
                      <span aria-hidden="true">{e.is_dir ? "▸ " : ""}</span>
                      {e.name}
                      {e.symlink && <span className="muted"> → {e.symlink}</span>}
                    </button>
                    {e.outside && <span className="muted text-xs"> {t("agent.files.outside")}</span>}
                  </td>
                  <td className="muted mono text-xs">{e.is_dir ? "—" : fmtBytes(e.size)}</td>
                  <td className="muted text-xs">{new Date(e.mod_time).toLocaleString()}</td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    {!e.is_dir && !e.outside && (
                      <a
                        className="btn sm"
                        href={`/api/v1/agents/${agent.id}/files/download?path=${q(e.path)}`}
                        download={e.name}
                      >
                        {t("agent.files.download")}
                      </a>
                    )}
                    {canWrite && (
                      <>
                        {" "}
                        <button className="btn sm" disabled={busyNow} onClick={() => setPrompting({ kind: "rename", entry: e })}>
                          {t("agent.files.rename")}
                        </button>{" "}
                        <button className="btn sm danger" disabled={busyNow} onClick={() => setConfirming(e)}>
                          {t("agent.files.delete")}
                        </button>
                      </>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
        {listing.data?.truncated && (
          <p className="muted text-xs" style={{ padding: "8px 14px" }}>
            {t("agent.files.tooManyEntries")}
          </p>
        )}
      </div>

      {canWrite && (
        <p className="muted text-xs mt-2">{t("agent.files.dropHint")}</p>
      )}

      {openFile && (
        <FileViewer
          agentId={agent.id}
          path={openFile}
          canWrite={canWrite}
          onClose={() => setParam("file", null)}
          onSaved={inval}
        />
      )}

      {prompting && (
        <NameDialog
          title={
            prompting.kind === "mkdir"
              ? t("agent.files.newFolder")
              : prompting.kind === "newfile"
                ? t("agent.files.newFile")
                : t("agent.files.renameTitle", { name: prompting.entry?.name })
          }
          initial={prompting.kind === "rename" ? (prompting.entry?.name ?? "") : ""}
          label={prompting.kind === "rename" ? t("agent.files.newName") : t("agent.files.name")}
          pending={busyNow}
          onClose={() => setPrompting(null)}
          onSubmit={(name) => {
            const target = dir ? `${dir}/${name}` : name;
            const kind = prompting.kind;
            const entry = prompting.entry;
            setPrompting(null);
            run(async () => {
              if (kind === "mkdir") await post(`/agents/${agent.id}/files/dir`, { path: target });
              else if (kind === "newfile")
                await put(`/agents/${agent.id}/files/content`, { path: target, content: "" });
              else if (entry) await post(`/agents/${agent.id}/files/move`, { from: entry.path, to: target });
            });
          }}
        />
      )}

      {confirming && (
        <ConfirmDialog
          title={t("agent.files.deleteTitle", { name: confirming.name })}
          pending={remove.isPending}
          onClose={() => setConfirming(null)}
          onConfirm={() => remove.mutate(confirming)}
        >
          <p className="text-sm">
            {confirming.is_dir
              ? t("agent.files.deleteDirConfirm", { name: confirming.name })
              : t("agent.files.deleteFileConfirm", { name: confirming.name })}
          </p>
        </ConfirmDialog>
      )}
    </div>
  );
}

// FileViewer zeigt eine Datei und lässt sie ändern. Binäres bleibt zu — im
// Textfeld würde es zu Müll, und beim Speichern zu kaputtem Müll.
function FileViewer({
  agentId,
  path,
  canWrite,
  onClose,
  onSaved,
}: {
  agentId: string;
  path: string;
  canWrite: boolean;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useTranslation();
  const file = useQuery({
    queryKey: ["agent-file", agentId, path],
    queryFn: () => api<FileContent>(`/agents/${agentId}/files/content?path=${q(path)}`),
    retry: false,
    gcTime: 0,
    staleTime: 0,
    refetchOnMount: "always",
  });
  const [draft, setDraft] = useState<string | null>(null);
  useEffect(() => setDraft(null), [path]);

  const save = useMutation({
    mutationFn: (content: string) => put(`/agents/${agentId}/files/content`, { path, content }),
    onSuccess: () => {
      setDraft(null);
      file.refetch();
      onSaved();
    },
  });

  const d = file.data;
  const editable = canWrite && d && !d.binary && !d.truncated;
  const value = draft ?? d?.content ?? "";
  const dirty = draft !== null && draft !== d?.content;

  return (
    <Modal
      title={path}
      size="lg"
      onClose={onClose}
      footer={
        <>
          <a className="btn sm" href={`/api/v1/agents/${agentId}/files/download?path=${q(path)}`} download>
            {t("agent.files.download")}
          </a>
          <span className="ml-auto" />
          <button className="btn sm" onClick={onClose}>
            {t("modal.cancel")}
          </button>
          {editable && (
            <button
              className="btn sm primary"
              disabled={!dirty || save.isPending}
              onClick={() => save.mutate(value)}
            >
              {save.isPending ? t("agent.files.saving") : t("agent.files.save")}
            </button>
          )}
        </>
      }
    >
      {file.isLoading && <p className="muted text-sm">…</p>}
      {file.isError && <p className="danger-text text-sm">{(file.error as Error).message}</p>}
      {d && (
        <>
          <p className="muted text-xs mb-2 mono">
            {fmtBytes(d.size)} · {d.mode} · {new Date(d.mod_time).toLocaleString()}
          </p>
          {d.binary && <p className="text-sm">{t("agent.files.binary")}</p>}
          {!d.binary && d.truncated && (
            <p className="text-xs mb-2" style={{ color: "var(--text-warning)" }}>
              {t("agent.files.truncated")}
            </p>
          )}
          {!d.binary && (
            <textarea
              className="code"
              rows={22}
              style={{ width: "100%" }}
              readOnly={!editable}
              value={value}
              onChange={(e) => setDraft(e.target.value)}
            />
          )}
          {save.isError && <p className="danger-text text-xs">{(save.error as Error).message}</p>}
        </>
      )}
    </Modal>
  );
}

// Ein Name, mehr nicht — für „neuer Ordner", „neue Datei" und „umbenennen".
function NameDialog({
  title,
  label,
  initial,
  pending,
  onSubmit,
  onClose,
}: {
  title: string;
  label: string;
  initial: string;
  pending: boolean;
  onSubmit: (name: string) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(initial);
  const ok = name.trim() !== "" && !name.includes("/");
  return (
    <Modal
      title={title}
      size="sm"
      onClose={onClose}
      footer={
        <>
          <button className="btn sm" onClick={onClose}>
            {t("modal.cancel")}
          </button>
          <button className="btn sm primary" disabled={!ok || pending} onClick={() => onSubmit(name.trim())}>
            {t("modal.ok")}
          </button>
        </>
      }
    >
      <label>{label}</label>
      <input
        autoFocus
        value={name}
        onChange={(e) => setName(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter" && ok && !pending) onSubmit(name.trim());
        }}
      />
      {name.includes("/") && <p className="muted text-xs mt-2">{t("agent.files.noSlash")}</p>}
    </Modal>
  );
}
