import { useState } from "react";
import { useTranslation } from "react-i18next";
import type { ChatTone } from "../api";

const NOTE_MAX = 300;

/* How an agent talks in the team chat (#457): three choices and a line of
 * free text. The same form on a voice and in the organisation's settings —
 * the organisation's values are the default a voice's empty fields fall back
 * to, so "default" is a choice on both. */
export function ChatToneForm({
  value,
  editable,
  saving,
  error,
  onSave,
}: {
  value: ChatTone;
  editable: boolean;
  saving: boolean;
  error?: string;
  onSave: (t: ChatTone) => void;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState<ChatTone>(value);
  const [saved, setSaved] = useState(false);
  const set = (k: keyof ChatTone, v: string) => {
    setSaved(false);
    setDraft({ ...draft, [k]: v });
  };
  const choice = (k: "address" | "tone" | "emoji", options: string[]) => (
    <label className="text-xs">
      <div className="muted mb-1">{t(`chatTone.${k}`)}</div>
      <select value={draft[k] ?? ""} disabled={!editable} onChange={(e) => set(k, e.target.value)}>
        <option value="">{t("chatTone.unset")}</option>
        {options.map((o) => (
          <option key={o} value={o}>
            {t(`chatTone.${k}_${o}`)}
          </option>
        ))}
      </select>
    </label>
  );

  return (
    <div className="flex flex-col gap-2" style={{ maxWidth: 680 }}>
      <div className="flex gap-3 flex-wrap items-end">
        {choice("address", ["du", "sie", "auto"])}
        {choice("tone", ["casual", "matter_of_fact", "formal"])}
        {choice("emoji", ["never", "sparingly", "freely"])}
      </div>
      <label className="text-xs">
        <div className="muted mb-1">{t("chatTone.note")}</div>
        <input
          style={{ width: "100%" }}
          maxLength={NOTE_MAX}
          value={draft.note ?? ""}
          readOnly={!editable}
          placeholder={t("chatTone.notePlaceholder")}
          onChange={(e) => set("note", e.target.value)}
        />
      </label>
      {editable && (
        <div className="flex gap-2 items-center">
          <button
            className="btn sm"
            disabled={saving}
            onClick={() => {
              onSave(draft);
              setSaved(true);
            }}
          >
            {t("chatTone.save")}
          </button>
          {saved && !saving && !error && <span className="muted text-xs">{t("chatTone.saved")}</span>}
          {error && (
            <span className="text-xs" style={{ color: "var(--error)" }}>
              {error}
            </span>
          )}
        </div>
      )}
    </div>
  );
}
