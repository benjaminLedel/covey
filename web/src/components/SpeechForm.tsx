import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, type OfferedVoice, type VoiceSpeech } from "../api";

/* How the agents carrying a voice sound when a call speaks their words
 * (#497). Two sources: a voice this instance offers the apps, synthesised on
 * the device, or the organisation's own speech server (only when one is
 * set). Left unset, the Mac app picks a voice per agent. */
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
    queryFn: () => api<{ voices?: OfferedVoice[]; synthesize?: boolean }>("/speech/model"),
  });
  const voices = offered.data?.voices ?? [];
  const server = !!offered.data?.synthesize;
  const [source, setSource] = useState<"" | "device" | "server">(value?.source ?? "");
  const [model, setModel] = useState(value?.source === "device" ? value.model : "");
  const [serverModel, setServerModel] = useState(value?.source === "server" ? value.model : "");
  const [serverVoice, setServerVoice] = useState(value?.source === "server" ? (value.voice ?? "") : "");
  const [speaker, setSpeaker] = useState(value?.speaker ?? 0);
  const [rate, setRate] = useState(value?.rate || 1);
  const [saved, setSaved] = useState(false);
  const picked = voices.find((v) => v.name === model);
  const speakers = picked?.voice.speakers ?? 1;
  // A stored voice the instance no longer offers stays visible as what is stored.
  const stale = source === "device" && model !== "" && !picked && !offered.isLoading;
  const touch = () => setSaved(false);

  const save = () => {
    setSaved(true);
    const r = Math.abs(rate - 1) < 0.001 ? undefined : rate;
    if (source === "device") return onSave({ source, model, speaker: Math.min(speaker, speakers - 1), rate: r });
    if (source === "server") return onSave({ source, model: serverModel.trim(), voice: serverVoice.trim() || undefined, speaker: 0, rate: r });
    onSave({});
  };
  const ready = source === "" || (source === "device" ? model !== "" : serverModel.trim() !== "");

  return (
    <div className="flex flex-col gap-2" style={{ maxWidth: 680 }}>
      <div className="flex gap-3 flex-wrap items-end">
        <label className="text-xs">
          <div className="muted mb-1">{t("voiceSpeech.source")}</div>
          <select
            value={source}
            disabled={!editable}
            onChange={(e) => {
              touch();
              setSource(e.target.value as "" | "device" | "server");
            }}
          >
            <option value="">{t("voiceSpeech.unset")}</option>
            <option value="device">{t("voiceSpeech.device")}</option>
            {(server || source === "server") && <option value="server">{t("voiceSpeech.server")}</option>}
          </select>
        </label>
        {source === "device" && (
          <label className="text-xs">
            <div className="muted mb-1">{t("voiceSpeech.voice")}</div>
            <select
              value={model}
              disabled={!editable}
              onChange={(e) => {
                touch();
                setModel(e.target.value);
                setSpeaker(0);
              }}
            >
              <option value="">{t("voiceSpeech.pick")}</option>
              {voices.map((v) => (
                <option key={v.name} value={v.name}>
                  {v.voice.label} · {v.voice.language}
                  {v.voice.placeholder ? ` · ${t("voiceSpeech.placeholder")}` : ""}
                </option>
              ))}
              {stale && <option value={model}>{model}</option>}
            </select>
          </label>
        )}
        {source === "device" && picked && speakers > 1 && (
          <label className="text-xs">
            <div className="muted mb-1">{t("voiceSpeech.speaker")}</div>
            <input
              type="number"
              min={0}
              max={speakers - 1}
              value={speaker}
              readOnly={!editable}
              onChange={(e) => {
                touch();
                setSpeaker(Math.max(0, Math.min(speakers - 1, Number(e.target.value) || 0)));
              }}
            />
          </label>
        )}
        {source === "server" && (
          <>
            <label className="text-xs">
              <div className="muted mb-1">{t("voiceSpeech.serverModel")}</div>
              <input
                value={serverModel}
                maxLength={100}
                readOnly={!editable}
                onChange={(e) => {
                  touch();
                  setServerModel(e.target.value);
                }}
              />
            </label>
            <label className="text-xs">
              <div className="muted mb-1">{t("voiceSpeech.serverVoice")}</div>
              <input
                value={serverVoice}
                placeholder="DEFAULT_VOICE"
                maxLength={100}
                readOnly={!editable}
                onChange={(e) => {
                  touch();
                  setServerVoice(e.target.value);
                }}
              />
            </label>
          </>
        )}
        {source !== "" && (
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
                touch();
                setRate(Number(e.target.value));
              }}
            />
          </label>
        )}
      </div>
      {source === "device" && picked && <div className="muted text-xs">{picked.voice.licence}</div>}
      {source === "device" && !offered.isLoading && voices.length === 0 && (
        <div className="muted text-xs">{t("voiceSpeech.noneOffered")}</div>
      )}
      {editable && (
        <div className="flex gap-2 items-center">
          <button className="btn sm" disabled={saving || !ready} onClick={save}>
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
