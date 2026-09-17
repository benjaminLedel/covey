import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Modal } from "./Modal";
import { SKILL_ENTRY, type Skill, type SkillFile } from "../api";

// The editor for a skill — the same form for library and agent,
// for creating as for changing.
//
// Two things here are deliberately this way and not the other:
//
//   - The name is locked when changing. It is the directory name in
//     the agent home and thereby the /slash-command that other texts
//     refer to; the API rejects a rename accordingly (409).
//   - SKILL.md cannot be removed. Without it the runtime does not know the
//     directory as a skill — the agent would silently get nothing at all.
export type SkillDraft = { name: string; description: string; files: SkillFile[] };

export function SkillEditor({
  skill,
  title,
  saving,
  error,
  onSave,
  onClose,
}: {
  /** Existing skill to change; if it is missing, one is created. */
  skill?: Skill;
  title: string;
  saving: boolean;
  error?: string;
  onSave: (draft: SkillDraft) => void;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState(skill?.name ?? "");
  const [description, setDescription] = useState(skill?.description ?? "");
  const [files, setFiles] = useState<SkillFile[]>(
    skill?.files?.length ? skill.files : [{ path: SKILL_ENTRY, content: "" }],
  );
  const [newPath, setNewPath] = useState("");
  const isNew = !skill;

  const setContent = (path: string, content: string) =>
    setFiles((fs) => fs.map((f) => (f.path === path ? { ...f, content } : f)));

  const addFile = () => {
    const path = newPath.trim();
    if (!path || files.some((f) => f.path === path)) return;
    setFiles((fs) => [...fs, { path, content: "" }]);
    setNewPath("");
  };

  return (
    <Modal
      title={title}
      size="lg"
      onClose={onClose}
      footer={
        <div className="flex items-center gap-3">
          <button
            className="btn primary sm"
            disabled={saving || !name.trim() || !description.trim()}
            onClick={() => onSave({ name: name.trim(), description: description.trim(), files })}
          >
            {saving ? "…" : t("skills.save")}
          </button>
          <button className="btn sm" onClick={onClose}>
            {t("skills.cancel")}
          </button>
          {error && <span className="danger-text text-xs">{error}</span>}
        </div>
      }
    >
      <div className="flex gap-3 flex-wrap mb-3">
        <div className="min-w-52">
          <label>{t("skills.name")}</label>
          <input
            className="mono"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="ticket-aufbereiten"
            disabled={!isNew}
            title={isNew ? undefined : t("skills.nameLocked")}
            required
          />
        </div>
      </div>
      <div className="mb-3">
        <label>{t("skills.description")}</label>
        <textarea
          rows={2}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder={t("skills.descriptionPlaceholder")}
          required
        />
        <p className="muted text-xs m-0">{t("skills.descriptionHint")}</p>
      </div>

      {files.map((f) => (
        <div key={f.path} className="mb-3">
          <div className="flex items-center gap-2">
            <label className="mono flex-1">{f.path}</label>
            {f.path !== SKILL_ENTRY && (
              <button
                className="btn sm"
                type="button"
                onClick={() => setFiles((fs) => fs.filter((x) => x.path !== f.path))}
              >
                {t("skills.removeFile")}
              </button>
            )}
          </div>
          <textarea
            className="code"
            rows={Math.min(16, Math.max(6, f.content.split("\n").length + 1))}
            value={f.content}
            onChange={(e) => setContent(f.path, e.target.value)}
          />
        </div>
      ))}

      <div className="flex gap-2 items-end">
        <div className="min-w-52">
          <label>{t("skills.addFile")}</label>
          <input
            className="mono"
            value={newPath}
            onChange={(e) => setNewPath(e.target.value)}
            placeholder="referenz.md"
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                addFile();
              }
            }}
          />
        </div>
        <button className="btn sm" type="button" onClick={addFile} disabled={!newPath.trim()}>
          {t("skills.addFileBtn")}
        </button>
        <p className="muted text-xs m-0 flex-1">{t("skills.filesHint")}</p>
      </div>
    </Modal>
  );
}
