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
import { Markdown } from "./Markdown";
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

// Ein hineingezogener Ordner ist im Browser kein File, sondern ein
// FileSystemEntry-Baum, den man selbst ablaufen muss. Ohne das käme beim
// Ziehen eines Ordners genau nichts an — dataTransfer.files ist dann leer.
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
  // readEntries liefert je Aufruf nur einen Teil (Chrome: 100) — bis zur
  // leeren Antwort weiterlesen, sonst fehlt beim großen Ordner der Rest.
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
  // Browser ohne webkitGetAsEntry: wenigstens die flachen Dateien.
  return Array.from(dt.files).map((file) => ({ file, path: file.name }));
}

// Symbole im Strichstil der übrigen UI. Ein Ordner sieht anders aus als ein
// Bild — das erspart es, jede Zeile zu lesen, um die Liste zu überfliegen.
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
  const [uploading, setUploading] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [prompting, setPrompting] = useState<null | { kind: "mkdir" | "newfile" | "rename"; entry?: FileEntry }>(null);
  const [confirming, setConfirming] = useState<FileEntry | null>(null);
  // Auswahl fürs Sammel-Herunterladen. Sie hängt am Ordner: wer weiterklickt,
  // nimmt sie nicht versehentlich mit.
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

  // uploadFiles nimmt Dateien samt ihrem relativen Pfad: der dritte Parameter
  // von append() ist der Dateiname im Upload, und der darf hier Ordner
  // enthalten. So kommt ein hineingezogener Ordner drüben genauso wieder an,
  // statt als Haufen loser Dateien. Der Server setzt den Pfad zusammen und
  // normalisiert ihn — aus dem Home führt auch so keiner hinaus.
  const uploadFiles = (files: Array<{ file: File; path: string }>) => {
    if (files.length === 0) return;
    const form = new FormData();
    for (const { file, path } of files) form.append("file", file, path);
    setUploading(files.length);
    return run(() => upload(`/agents/${agent.id}/files/upload?path=${q(dir)}`, form)).finally(() =>
      setUploading(0),
    );
  };

  // Aus einem <input> kommen Dateien flach — außer bei einem Ordner-Upload,
  // dann trägt webkitRelativePath die Struktur.
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
  // Mehrere Pfade in einer URL: derselbe Parameter mehrfach — so liest ihn der
  // Handler als Liste, ohne ein eigenes Trennzeichen zu erfinden, das in einem
  // Dateinamen vorkommen könnte.
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
            {/* webkitdirectory ist kein Standard-Attribut, React reicht es aber
                durch — ohne es gibt es keinen Ordner-Upload über den Dialog. */}
            <input
              ref={dirInput}
              type="file"
              multiple
              hidden
              // @ts-expect-error — nicht im React-Typ, von allen Browsern unterstützt
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
          // filesFromDrop greift die Items noch vor seinem ersten await ab —
          // danach räumt der Browser die DataTransfer-Liste ab, die daraus
          // gewonnenen Entry-Objekte bleiben aber gültig.
          filesFromDrop(e.dataTransfer).then(uploadFiles);
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
                      // Ein Ordner kommt als Archiv, eine Datei roh — beides
                      // unter demselben Knopf, weil es dieselbe Absicht ist.
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

// FileViewer zeigt eine Datei — jede Art so, wie man sie ansehen will:
// Markdown gerendert, Bilder als Bild, PDF eingebettet, Tabellen als Tabelle,
// alles andere im Editor. Wo es einen Quelltext gibt, ist er einen Klick
// entfernt und bleibt bearbeitbar; die Vorschau ersetzt den Editor nicht,
// sondern steht davor. Binäres bleibt zu — im Textfeld würde es zu Müll, und
// beim Speichern zu kaputtem Müll.
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
  // Quelltext statt Vorschau — die Wahl gilt fürs Fenster, nicht für die Datei.
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
  // Gerendert werden kann, was Text ist und eine eigene Darstellung hat. Ein
  // angefangener Edit zieht die Vorschau mit: man will sehen, was man tippt.
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
                // Karierter Grund: sonst ist bei einem transparenten PNG nicht
                // zu sehen, wo das Bild aufhört und die Seite anfängt.
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

// CsvTable zeigt eine Tabellendatei als Tabelle. Der Parser kann, was das
// Format wirklich braucht: Trennzeichen nach Endung, Anführungszeichen mit
// verdoppelten Quotes und Trennern im Feld. Zeilenumbrüche INNERHALB eines
// Feldes kann er nicht — dafür gibt es den Quelltext, und die Tabelle sagt es
// nicht, weil sie es gar nicht erst falsch darstellt: solche Zeilen enden
// sichtbar als eigene Zeile.
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
