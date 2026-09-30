import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, type OfferedVoice, type VoiceSpeech } from "../api";

/* How the agents carrying a voice sound when a call speaks their words
 * (#497): one of the voices this instance offers the apps, or the device's
 * own synthesis. Left unset, the Mac app picks a voice per agent. */
export function SpeechForm({
  value,
  editable,
  saving,
  error,
  onSave,
}: {
  value: VoiceSpeech | null | undefined;
  editable: boolean;
  saving: boolean;
  error?: string;
  onSave: (s: VoiceSpeech | Record<string, never>) => void;
}) {
  const { t } = useTranslation();
  const offered = useQuery({
    queryKey: ["speech-voices"],
    queryFn: () => api<{ voices?: OfferedVoice[] }>("/speech/model"),
  });
  const voices = offered.data?.voices ?? [];
  // "" unset, "system", or the name of an offered voice.
  const [choice, setChoice] = useState(value ? (value.engine === "system" ? "system" : (value.model ?? "")) : "");
  const [speaker, setSpeaker] = useState(value?.speaker ?? 0);
  const [rate, setRate] = useState(value?.rate || 1);
  const [saved, setSaved] = useState(false);
  const picked = voices.find((v) => v.name === choice);
  const speakers = picked?.voice.speakers ?? 1;
  // A voice the instance no longer offers stays visible as what is stored.
  const stale = choice !== "" && choice !== "system" && !picked && !offered.isLoading;

  const save = () => {
    setSaved(true);
    if (choice === "") return onSave({});
    const r = Math.abs(rate - 1) < 0.001 ? undefined : rate;
    onSave(
      choice === "system"
        ? { engine: "system", speaker: 0, rate: r }
        : { engine: "sherpa-onnx", model: choice, speaker: Math.min(speaker, speakers - 1), rate: r },
    );
  };

  return (
    <div className="flex flex-col gap-2" style={{ maxWidth: 680 }}>
      <div className="flex gap-3 flex-wrap items-end">
        <label className="text-xs">
          <div className="muted mb-1">{t("voiceSpeech.voice")}</div>
          <select
            value={choice}
            disabled={!editable}
            onChange={(e) => {
              setSaved(false);
              setChoice(e.target.value);
              setSpeaker(0);
            }}
          >
            <option value="">{t("voiceSpeech.unset")}</option>
            <option value="system">{t("voiceSpeech.system")}</option>
            {voices.map((v) => (
              <option key={v.name} value={v.name}>
                {v.voice.label} · {v.voice.language}
                {v.voice.placeholder ? ` · ${t("voiceSpeech.placeholder")}` : ""}
              </option>
            ))}
            {stale && <option value={choice}>{choice}</option>}
          </select>
        </label>
        {picked && speakers > 1 && (
          <label className="text-xs">
            <div className="muted mb-1">{t("voiceSpeech.speaker")}</div>
            <input
              type="number"
              min={0}
              max={speakers - 1}
              value={speaker}
              readOnly={!editable}
              onChange={(e) => {
                setSaved(false);
                setSpeaker(Math.max(0, Math.min(speakers - 1, Number(e.target.value) || 0)));
              }}
            />
          </label>
        )}
        {choice !== "" && (
          <label className="text-xs">
            <div className="muted mb-1">{t("voiceSpeech.rate", { rate: rate.toFixed(1) })}</div>
            <input
              type="range"
              min={0.5}
              max={2}
              step={0.1}
              value={rate}
              disabled={!editable}
              onChange={(e) => {
                setSaved(false);
                setRate(Number(e.target.value));
              }}
            />
          </label>
        )}
      </div>
      {picked && <div className="muted text-xs">{picked.voice.licence}</div>}
      {!offered.isLoading && voices.length === 0 && <div className="muted text-xs">{t("voiceSpeech.noneOffered")}</div>}
      {editable && (
        <div className="flex gap-2 items-center">
          <button className="btn sm" disabled={saving} onClick={save}>
            {t("voiceSpeech.save")}
          </button>
          {saved && !saving && !error && <span className="muted text-xs">{t("voiceSpeech.saved")}</span>}
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
