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
  type FilesUsage,
} from "../api";
import { fmtBytes } from "../format";
import { Markdown } from "./Markdown";
import { Modal, ConfirmDialog } from "./Modal";

// The agent's workstation: its persistent home as a file tree. It is
// the answer to "what does this one have lying around?" — until now only reachable
// through a shell on the host. Read, upload, edit, delete; the home
// outlives the sandbox, the browser therefore also works on a sleeping
// agent.
//
// The current folder and the open file stand in the URL (?dir=&file=),
// as everywhere in this view: a link to a file in an agent's home
// is something you send to someone.

const q = (s: string) => encodeURIComponent(s);

// A folder dragged in is not a File in the browser but a
// FileSystemEntry tree that you have to walk yourself. Without this, dragging a
// folder arrives as exactly nothing — dataTransfer.files is empty then.
type DTEntry = {
  isFile: boolean;
  isDirectory: boolean;
  name: string;
  file: (cb: (f: File) => void, err: (e: unknown) => void) => void;
  createReader: () => { readEntries: (cb: (e: DTEntry[]) => void, err: (e: unknown) => void) => void };
};

async function walkDropEntry(entry: DTEntry, prefix: string): Promise<Array<{ file: File; path: string }>> {
  if (entry.isFile) {
    const file = await new Promise<File>((res, rej) => entry.file(res, rej));
    return [{ file, path: prefix + entry.name }];
  }
  if (!entry.isDirectory) return [];
  const reader = entry.createReader();
  const out: Array<{ file: File; path: string }> = [];
  // readEntries returns only part of them per call (Chrome: 100) — read on until
  // the empty answer, otherwise the large folder is missing the rest.
  for (;;) {
    const batch = await new Promise<DTEntry[]>((res, rej) => reader.readEntries(res, rej));
    if (batch.length === 0) break;
    for (const child of batch) out.push(...(await walkDropEntry(child, prefix + entry.name + "/")));
  }
  return out;
}

async function filesFromDrop(dt: DataTransfer): Promise<Array<{ file: File; path: string }>> {
  const items = Array.from(dt.items ?? []);
  const entries = items
    .map((i) => (i.webkitGetAsEntry?.() as unknown as DTEntry | null) ?? null)
    .filter((e): e is DTEntry => e !== null);
  if (entries.length > 0) {
    const nested = await Promise.all(entries.map((e) => walkDropEntry(e, "")));
    return nested.flat();
  }
  // Browsers without webkitGetAsEntry: at least the flat files.
  return Array.from(dt.files).map((file) => ({ file, path: file.name }));
}

// Icons in the line style of the rest of the UI. A folder looks different than an
// image — that spares you reading every row to skim the list.
const GLYPHS: Record<string, string> = {
  dir: "M3 6a1 1 0 0 1 1-1h5l2 2h8a1 1 0 0 1 1 1v10a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6z",
  file: "M6 3h7l5 5v13H6V3zm7 0v5h5",
  markdown: "M6 3h7l5 5v13H6V3zm7 0v5h5M9 18v-5l2 2 2-2v5",
  image: "M4 5h16v14H4V5zm3 9l3-3 3 3 2-2 3 3M9 9.5a1 1 0 1 1-2 0 1 1 0 0 1 2 0z",
  pdf: "M6 3h7l5 5v13H6V3zm7 0v5h5M9 17v-4h1.5a1.2 1.2 0 0 1 0 2.4H9m5 1.6v-4h1.6",
  csv: "M4 5h16v14H4V5zm0 4.7h16M4 14.3h16M9.3 5v14M14.7 5v14",
};

function FileGlyph({ entry }: { entry: FileEntry }) {
  const name = entry.is_dir ? "dir" : (entry.preview && GLYPHS[entry.preview]) ? entry.preview : "file";
  return (
    <svg viewBox="0 0 24 24" className="file-ic" aria-hidden="true">
      <path d={GLYPHS[name]} />
    </svg>
  );
}

// The space gauge. The reason it exists: the persistent home is
// deliberate (caches survive, the next run starts warm), but nobody
// could see the consequence. A QA agent wrote into its own wiki
// that its 40 G overlay was filling up through old checkouts — shortly after, a
// run ended with `claude exit: signal: killed`. The information existed, just not here.
function DiskGauge({ usage, onOpenRepos }: { usage?: FilesUsage; onOpenRepos: () => void }) {
  const { t } = useTranslation();
  if (!usage?.exists || usage.total_bytes <= 0) return null;
  const used = usage.total_bytes - usage.free_bytes;
  const pct = Math.min(100, Math.round((used / usage.total_bytes) * 100));
  // From 85 % it gets tight: an npm install or a checkout needs headroom.
  const tight = pct >= 85;
  const top = usage.checkouts.slice(0, 3);
  return (
    <div className="card mb-3" style={{ padding: "10px 14px" }}>
      <div className="flex items-baseline gap-2 flex-wrap text-xs">
        <span style={{ fontWeight: 600 }}>{t("agent.files.disk")}</span>
        <span className={tight ? "" : "muted"} style={tight ? { color: "var(--text-warning)" } : undefined}>
          {t("agent.files.diskUsed", {
            used: fmtBytes(used),
            total: fmtBytes(usage.total_bytes),
            pct,
          })}
        </span>
        {usage.checkouts.length > 0 && (
          <button className="btn sm" style={{ border: "none" }} onClick={onOpenRepos}>
            {t("agent.files.diskCheckouts", {
              count: usage.checkouts.length,
              size: fmtBytes(usage.checkout_bytes),
            })}
          </button>
        )}
      </div>
      <div style={{ height: 6, background: "var(--surface-1)", borderRadius: 3, overflow: "hidden", marginTop: 6 }}>
        <div
          style={{
            width: `${pct}%`,
            height: "100%",
            background: tight ? "var(--text-warning)" : "var(--text-accent)",
          }}
        />
      </div>
      {top.length > 0 && (
        <div className="muted text-xs mono" style={{ marginTop: 6 }}>
          {top.map((c) => `${c.name} ${fmtBytes(c.bytes)}`).join(" · ")}
        </div>
      )}
    </div>
  );
}

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
        if (key === "dir") n.delete("file"); // a folder change closes the file
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

  const usage = useQuery({
    queryKey: ["agent-files-usage", agent.id],
    queryFn: () => api<FilesUsage>(`/agents/${agent.id}/files/usage`),
    retry: false,
  });

  const [dropping, setDropping] = useState(false);
  const [busy, setBusy] = useState(false);
  const [uploading, setUploading] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [prompting, setPrompting] = useState<null | { kind: "mkdir" | "newfile" | "rename"; entry?: FileEntry }>(null);
  const [confirming, setConfirming] = useState<FileEntry | null>(null);
  // Selection for the bulk download. It hangs on the folder: whoever clicks
  // onward does not take it along by accident.
  const [selected, setSelected] = useState<Set<string>>(new Set());
  useEffect(() => setSelected(new Set()), [dir]);
  const fileInput = useRef<HTMLInputElement>(null);
  const dirInput = useRef<HTMLInputElement>(null);

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

  // uploadFiles takes files along with their relative path: the third parameter
  // of append() is the filename in the upload, and here it may contain folders.
  // That way a dragged folder arrives on the other side the same way,
  // instead of as a pile of loose files. The server puts the path together and
  // normalizes it — this way none of it leads out of the home either.
  const uploadFiles = (files: Array<{ file: File; path: string }>) => {
    if (files.length === 0) return;
    const form = new FormData();
    for (const { file, path } of files) form.append("file", file, path);
    setUploading(files.length);
    return run(() => upload(`/agents/${agent.id}/files/upload?path=${q(dir)}`, form)).finally(() =>
      setUploading(0),
    );
  };

  // Out of an <input> files come flat — except for a folder upload,
  // then webkitRelativePath carries the structure.
  const uploadPicked = (list: FileList) =>
    uploadFiles(
      Array.from(list).map((file) => ({
        file,
        path: (file as File & { webkitRelativePath?: string }).webkitRelativePath || file.name,
      })),
    );

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
  const entries = listing.data?.entries ?? [];
  const selectable = entries.filter((e) => !e.outside);
  const allSelected = selectable.length > 0 && selectable.every((e) => selected.has(e.path));
  const toggle = (path: string) =>
    setSelected((prev) => {
      const n = new Set(prev);
      n.has(path) ? n.delete(path) : n.add(path);
      return n;
    });
  // Several paths in one URL: the same parameter repeatedly — that is how the
  // handler reads it as a list, without inventing a separator of its own that
  // could occur in a filename.
  const zipURL = (paths: string[]) =>
    `/api/v1/agents/${agent.id}/files/zip?` + paths.map((p) => `path=${q(p)}`).join("&");

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

      <DiskGauge usage={usage.data} onOpenRepos={() => setParam("dir", "repos")} />

      {/* Path bar and tools in one row: where you are and
          what you can do there belong together. */}
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
        {selected.size > 0 && (
          <>
            <span className="muted text-xs">{t("agent.files.selected", { count: selected.size })}</span>
            <a className="btn sm primary" href={zipURL([...selected])}>
              {t("agent.files.downloadZip")}
            </a>
            <button className="btn sm" onClick={() => setSelected(new Set())}>
              {t("agent.files.clearSelection")}
            </button>
          </>
        )}
        {canWrite && (
          <>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              onChange={(e) => {
                if (e.target.files) uploadPicked(e.target.files);
                e.target.value = "";
              }}
            />
            {/* webkitdirectory is no standard attribute, but React passes it
                through — without it there is no folder upload via the dialog. */}
            <input
              ref={dirInput}
              type="file"
              multiple
              hidden
              // @ts-expect-error — not in the React type, supported by all browsers
              webkitdirectory=""
              onChange={(e) => {
                if (e.target.files) uploadPicked(e.target.files);
                e.target.value = "";
              }}
            />
            <button className="btn sm primary" disabled={busyNow} onClick={() => fileInput.current?.click()}>
              {t("agent.files.upload")}
            </button>
            <button className="btn sm" disabled={busyNow} onClick={() => dirInput.current?.click()}>
              {t("agent.files.uploadFolder")}
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
      {uploading > 0 && (
        <p className="muted text-xs mb-2">{t("agent.files.uploadingFiles", { count: uploading })}</p>
      )}

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
          // filesFromDrop grabs the items before its first await —
          // after that the browser clears away the DataTransfer list, the
          // entry objects gained from it stay valid though.
          filesFromDrop(e.dataTransfer).then(uploadFiles);
        }}
      >
        {listing.isError && (
          <p className="danger-text text-xs" style={{ padding: "14px" }}>
            {(listing.error as Error).message}
          </p>
        )}
        {/* Read from the snapshot because the runner is not connected: this
            belongs before the upload attempt, not in the error after it. */}
        {listing.data?.read_only && (
          <p className="muted text-xs" style={{ padding: "10px 14px" }} title={listing.data.read_only_reason}>
            {t("agent.settings.filesReadOnly")}
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
                <th style={{ width: 28 }}>
                  <input
                    type="checkbox"
                    aria-label={t("agent.files.selectAll")}
                    checked={allSelected}
                    onChange={() =>
                      setSelected(allSelected ? new Set() : new Set(selectable.map((e) => e.path)))
                    }
                  />
                </th>
                <th>{t("agent.files.colName")}</th>
                <th style={{ width: 100 }}>{t("agent.files.colSize")}</th>
                <th style={{ width: 160 }}>{t("agent.files.colModified")}</th>
                <th style={{ width: 260 }} />
              </tr>
            </thead>
            <tbody>
              {listing.data.entries.map((e) => (
                <tr key={e.path}>
                  <td>
                    <input
                      type="checkbox"
                      aria-label={e.name}
                      disabled={e.outside}
                      checked={selected.has(e.path)}
                      onChange={() => toggle(e.path)}
                    />
                  </td>
                  <td style={{ maxWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                    <button
                      className="btn sm mono flex items-center gap-2"
                      style={{ border: "none", padding: "2px 4px", maxWidth: "100%" }}
                      disabled={e.outside}
                      title={e.symlink ? t("agent.files.symlinkTo", { target: e.symlink }) : e.name}
                      onClick={() => (e.is_dir ? setParam("dir", e.path) : setParam("file", e.path))}
                    >
                      <FileGlyph entry={e} />
                      {e.name}
                      {e.symlink && <span className="muted"> → {e.symlink}</span>}
                    </button>
                    {e.outside && <span className="muted text-xs"> {t("agent.files.outside")}</span>}
                  </td>
                  <td className="muted mono text-xs">{e.is_dir ? "—" : fmtBytes(e.size)}</td>
                  <td className="muted text-xs">{new Date(e.mod_time).toLocaleString()}</td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    {!e.outside && (
                      // A folder comes as an archive, a file raw — both
                      // under the same button, because it is the same intent.
                      <a
                        className="btn sm"
                        href={
                          e.is_dir
                            ? zipURL([e.path])
                            : `/api/v1/agents/${agent.id}/files/download?path=${q(e.path)}`
                        }
                        download={e.is_dir ? `${e.name}.zip` : e.name}
                      >
                        {e.is_dir ? t("agent.files.downloadZipShort") : t("agent.files.download")}
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

      {canWrite && <p className="muted text-xs mt-2">{t("agent.files.dropHint")}</p>}

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

// FileViewer shows a file — every kind the way you want to look at it:
// Markdown rendered, images as image, PDF embedded, tables as table,
// everything else in the editor. Where there is a source text, it is one click
// away and stays editable; the preview does not replace the editor,
// it stands in front of it. Binary stays closed — in the text field it would
// become garbage, and on saving broken garbage.
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
  // Source instead of preview — the choice is for the window, not for the file.
  const [source, setSource] = useState(false);
  useEffect(() => {
    setDraft(null);
    setSource(false);
  }, [path]);

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
  // Renderable is what is text and has a display of its own. An
  // started edit pulls the preview along: you want to see what you type.
  const renderable = d?.preview === "markdown" || d?.preview === "csv";
  const showPreview = renderable && !source;
  const previewURL = `/api/v1/agents/${agentId}/files/preview?path=${q(path)}`;

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
          {renderable && (
            <button className="btn sm" onClick={() => setSource((v) => !v)}>
              {source ? t("agent.files.showPreview") : t("agent.files.showSource")}
            </button>
          )}
          {(d?.preview === "image" || d?.preview === "pdf") && (
            <a className="btn sm" href={previewURL} target="_blank" rel="noopener noreferrer">
              {t("agent.files.openTab")}
            </a>
          )}
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
          {d.preview === "image" && (
            <img
              src={previewURL}
              alt={path}
              style={{
                maxWidth: "100%",
                maxHeight: "60vh",
                display: "block",
                margin: "0 auto",
                // Checkered ground: otherwise with a transparent PNG you cannot
                // see where the image ends and the page begins.
                background:
                  "repeating-conic-gradient(var(--surface-1) 0% 25%, transparent 0% 50%) 50% / 16px 16px",
              }}
            />
          )}
          {d.preview === "pdf" && (
            <iframe
              src={previewURL}
              title={path}
              style={{ width: "100%", height: "60vh", border: "0.5px solid var(--border)", borderRadius: 6 }}
            />
          )}
          {d.preview === "binary" && <p className="text-sm">{t("agent.files.binary")}</p>}
          {!d.binary && d.truncated && (
            <p className="text-xs mb-2" style={{ color: "var(--text-warning)" }}>
              {t("agent.files.truncated")}
            </p>
          )}
          {showPreview && d.preview === "markdown" && (
            <div className="md-body">
              <Markdown text={value} />
            </div>
          )}
          {showPreview && d.preview === "csv" && <CsvTable text={value} path={path} />}
          {!d.binary && !showPreview && (
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

// CsvTable shows a table file as a table. The parser can do what the
// format really needs: separator by extension, quotes with doubled
// quotes and separators inside the field. Line breaks INSIDE a
// field it cannot do — that is what the source text is for, and the table does not
// say so, because it does not represent them wrongly in the first place: such lines
// end visibly as their own row.
const CSV_MAX_ROWS = 200;

function parseDelimited(text: string, sep: string): string[][] {
  const rows: string[][] = [];
  for (const line of text.replace(/\r\n/g, "\n").split("\n")) {
    if (line === "" && rows.length > 0) continue;
    const cells: string[] = [];
    let cur = "";
    let quoted = false;
    for (let i = 0; i < line.length; i++) {
      const c = line[i];
      if (quoted) {
        if (c === '"' && line[i + 1] === '"') {
          cur += '"';
          i++;
        } else if (c === '"') quoted = false;
        else cur += c;
      } else if (c === '"' && cur === "") quoted = true;
      else if (c === sep) {
        cells.push(cur);
        cur = "";
      } else cur += c;
    }
    cells.push(cur);
    rows.push(cells);
    if (rows.length >= CSV_MAX_ROWS + 1) break;
  }
  return rows;
}

function CsvTable({ text, path }: { text: string; path: string }) {
  const { t } = useTranslation();
  const sep = path.toLowerCase().endsWith(".tsv") ? "\t" : ",";
  const rows = parseDelimited(text, sep);
  if (rows.length === 0) return <p className="muted text-sm">{t("agent.files.emptyFile")}</p>;
  const [head, ...body] = rows;
  const shown = body.slice(0, CSV_MAX_ROWS);

  return (
    <>
      <div style={{ overflowX: "auto", maxHeight: "60vh" }}>
        <table className="tbl">
          <thead>
            <tr>
              {head.map((c, i) => (
                <th key={i}>{c}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {shown.map((r, i) => (
              <tr key={i}>
                {head.map((_, j) => (
                  <td key={j} className="mono text-xs">
                    {r[j] ?? ""}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="muted text-xs mt-2">
        {body.length > shown.length
          ? t("agent.files.csvTruncated", { shown: shown.length, cols: head.length })
          : t("agent.files.csvRows", { rows: shown.length, cols: head.length })}
      </p>
    </>
  );
}

// A name, nothing more — for "new folder", "new file" and "rename".
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
