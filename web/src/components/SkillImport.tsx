import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Modal } from "./Modal";
import { importSkills, type SkillImportResult } from "../api";

// Bringing in skills that already exist as folders — a zip from someone's
// laptop, or a link to a repository. The same dialog for the library and for
// one agent; base is "" or "/agents/{id}".
//
// Two steps on purpose: first a check that stores nothing and lists what the
// archive holds, then the import of what is ticked. A repository often holds
// a dozen skills and an agent needs two of them; and a problem in one of them
// should be read before anything lands, not after.
export function SkillImport({
  base,
  title,
  onDone,
  onClose,
}: {
  base: string;
  title: string;
  onDone: () => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"url" | "file">("url");
  const [url, setUrl] = useState("");
  const [file, setFile] = useState<File | undefined>();
  const [replace, setReplace] = useState(false);
  const [check, setCheck] = useState<SkillImportResult | undefined>();
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | undefined>();
  const [done, setDone] = useState<number | undefined>();

  const source = mode === "url" ? { url: url.trim() } : { file };
  const ready = mode === "url" ? !!url.trim() : !!file;

  // Any change to the source or to "replace" invalidates the last check.
  const reset = () => {
    setCheck(undefined);
    setPicked(new Set());
    setError(undefined);
    setDone(undefined);
  };

  const runCheck = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await importSkills(base, source, { dryRun: true, replace });
      setCheck(res);
      setPicked(
        new Set(
          res.skills.filter((s) => s.problems.length === 0).map((s) => s.name),
        ),
      );
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const runImport = async () => {
    setBusy(true);
    setError(undefined);
    try {
      const res = await importSkills(base, source, {
        dryRun: false,
        replace,
        names: [...picked],
      });
      if (res.imported > 0) {
        setDone(res.imported);
        onDone();
      } else {
        setCheck(res);
      }
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const toggle = (name: string) =>
    setPicked((p) => {
      const n = new Set(p);
      if (n.has(name)) n.delete(name);
      else n.add(name);
      return n;
    });

  return (
    <Modal
      title={title}
      size="lg"
      onClose={onClose}
      footer={
        <div className="flex items-center gap-3">
          {done === undefined && !check && (
            <button
              className="btn primary sm"
              disabled={busy || !ready}
              onClick={runCheck}
            >
              {busy ? "…" : t("skills.importCheck")}
            </button>
          )}
          {done === undefined && check && (
            <button
              className="btn primary sm"
              disabled={busy || picked.size === 0}
              onClick={runImport}
            >
              {busy ? "…" : t("skills.importRun", { count: picked.size })}
            </button>
          )}
          <button className="btn sm" onClick={onClose}>
            {done === undefined ? t("skills.cancel") : t("skills.importClose")}
          </button>
          {error && <span className="danger-text text-xs">{error}</span>}
        </div>
      }
    >
      <p className="muted text-xs mt-0 mb-3">{t("skills.importIntro")}</p>
      <div className="flex gap-4 mb-3 text-sm">
        <label className="flex items-center gap-2" style={{ margin: 0 }}>
          <input
            type="radio"
            checked={mode === "url"}
            onChange={() => {
              setMode("url");
              reset();
            }}
            style={{ width: "auto" }}
          />
          {t("skills.importFromUrl")}
        </label>
        <label className="flex items-center gap-2" style={{ margin: 0 }}>
          <input
            type="radio"
            checked={mode === "file"}
            onChange={() => {
              setMode("file");
              reset();
            }}
            style={{ width: "auto" }}
          />
          {t("skills.importFromFile")}
        </label>
      </div>

      {mode === "url" ? (
        <div className="mb-3">
          <input
            className="mono"
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
              reset();
            }}
            placeholder="https://github.com/owner/repo/tree/main/skills/pdf"
          />
          <p className="muted text-xs m-0">{t("skills.importUrlHint")}</p>
        </div>
      ) : (
        <div className="mb-3">
          <input
            type="file"
            accept=".zip,application/zip"
            onChange={(e) => {
              setFile(e.target.files?.[0]);
              reset();
            }}
          />
        </div>
      )}

      <label
        className="flex items-center gap-2 text-sm mb-3"
        style={{ margin: 0 }}
      >
        <input
          type="checkbox"
          checked={replace}
          onChange={(e) => {
            setReplace(e.target.checked);
            reset();
          }}
          style={{ width: "auto" }}
        />
        {t("skills.importReplace")}
      </label>

      {done !== undefined && (
        <p className="text-sm">{t("skills.importDone", { count: done })}</p>
      )}

      {done === undefined &&
        check?.skills.map((s) => (
          <div
            key={s.dir + s.name}
            className="card mb-2"
            style={{ padding: "9px 13px" }}
          >
            <div className="flex items-center gap-3">
              <input
                type="checkbox"
                checked={picked.has(s.name)}
                disabled={s.problems.length > 0}
                onChange={() => toggle(s.name)}
                style={{ width: "auto" }}
              />
              <span className="mono text-sm">{s.name || s.dir}</span>
              {s.exists && (
                <span className="badge st-open">
                  {t("skills.importExists")}
                </span>
              )}
              {/* Two lines are enough to tell skills apart; published
                  descriptions run to a paragraph, the full text is the title. */}
              <span
                className="muted text-xs flex-1"
                title={s.description}
                style={{
                  display: "-webkit-box",
                  WebkitLineClamp: 2,
                  WebkitBoxOrient: "vertical",
                  overflow: "hidden",
                }}
              >
                {s.description}
              </span>
              <span className="muted text-xs">
                {t("skills.importFiles", { count: s.files })}
              </span>
            </div>
            {s.problems.map((p) => (
              <p key={p} className="danger-text text-xs mt-1 mb-0">
                {p}
              </p>
            ))}
            {s.skipped?.map((p) => (
              <p key={p} className="muted text-xs mt-1 mb-0">
                {t("skills.importSkipped", { what: p })}
              </p>
            ))}
          </div>
        ))}
    </Modal>
  );
}
