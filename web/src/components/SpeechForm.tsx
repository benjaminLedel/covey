import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, synthesizeSpeech, type VoiceSpeech } from "../api";
import { ProviderVoicePicker } from "./ProviderVoicePicker";

const INSTRUCTIONS_MAX = 300;

/* How the agents carrying a voice sound when a call speaks their words
 * (#497), through the organisation's voice provider — the one source of
 * covey's voices: the voice's name there, a short style hint, the speed.
 * Left empty, the agent gets one of the provider's voices assigned (#518),
 * in a style taken from the chat tone. Where the provider lists its voices
 * the name is chosen from that list. The preview plays a sentence through
 * the provider here in the browser, with the chosen voice. */
export function SpeechForm({
  value,
  name,
  editable,
  saving,
  error,
  onSave,
}: {
  value: VoiceSpeech | null | undefined;
  /** The covey voice's name, for the preview sentence. */
  name: string;
  editable: boolean;
  saving: boolean;
  error?: string;
  onSave: (s: VoiceSpeech) => void;
}) {
  const { t } = useTranslation();
  const provider = useQuery({
    queryKey: ["speech-provider"],
    queryFn: () => api<{ synthesize?: boolean }>("/speech/model"),
  });
  const available = !!provider.data?.synthesize;
  const [voice, setVoice] = useState(value?.voice ?? "");
  const [instructions, setInstructions] = useState(value?.instructions ?? "");
  const [speed, setSpeed] = useState(value?.speed || 1);
  const [saved, setSaved] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const audio = useRef<HTMLAudioElement | null>(null);
  const touch = () => setSaved(false);

  const stopPreview = () => {
    const a = audio.current;
    audio.current = null;
    if (a) {
      a.pause();
      URL.revokeObjectURL(a.src);
    }
    setPlaying(false);
  };
  useEffect(() => stopPreview, []);

  const current = (): VoiceSpeech => ({
    voice: voice.trim() || undefined,
    instructions: instructions.trim() || undefined,
    speed: Math.abs(speed - 1) < 0.001 ? undefined : speed,
  });

  const preview = async () => {
    stopPreview();
    setPreviewError("");
    setPlaying(true);
    try {
      const blob = await synthesizeSpeech({ text: t("voiceSpeech.sample", { name }), ...current() });
      const a = new Audio(URL.createObjectURL(blob));
      audio.current = a;
      a.onended = stopPreview;
      await a.play();
    } catch (e) {
      setPreviewError((e as Error).message);
      stopPreview();
    }
  };

  return (
    <div className="flex flex-col gap-2" style={{ maxWidth: 680 }}>
      <div className="flex gap-3 flex-wrap items-end">
        <ProviderVoicePicker
          label={t("voiceSpeech.voice")}
          value={voice}
          placeholder={t("voiceSpeech.voiceDefault")}
          readOnly={!editable}
          sampleName={name}
          onChange={(v) => {
            touch();
            setVoice(v);
          }}
        />
        <label className="text-xs">
          <div className="muted mb-1">{t("voiceSpeech.speed", { speed: speed.toFixed(2) })}</div>
          <input
            type="range"
            min={0.7}
            max={1.3}
            step={0.05}
            value={speed}
            disabled={!editable}
            onChange={(e) => {
              touch();
              setSpeed(Number(e.target.value));
            }}
          />
        </label>
      </div>
      <label className="text-xs">
        <div className="muted mb-1">{t("voiceSpeech.instructions")}</div>
        <input
          style={{ width: "100%" }}
          value={instructions}
          placeholder={t("voiceSpeech.instructionsPlaceholder")}
          maxLength={INSTRUCTIONS_MAX}
          readOnly={!editable}
          onChange={(e) => {
            touch();
            setInstructions(e.target.value);
          }}
        />
      </label>
      {!provider.isLoading && !available && <div className="muted text-xs">{t("voiceSpeech.noProvider")}</div>}
      <div className="flex gap-2 items-center">
        {available && (
          <button className="btn sm" onClick={playing ? stopPreview : preview}>
            {playing ? t("voiceSpeech.stop") : t("voiceSpeech.preview")}
          </button>
        )}
        {editable && (
          <button
            className="btn sm"
            disabled={saving}
            onClick={() => {
              setSaved(true);
              onSave(current());
            }}
          >
            {t("voiceSpeech.save")}
          </button>
        )}
        {saved && !saving && !error && <span className="muted text-xs">{t("voiceSpeech.saved")}</span>}
        {(error || previewError) && (
          <span className="text-xs" style={{ color: "var(--error)" }}>
            {error || previewError}
          </span>
        )}
      </div>
    </div>
  );
}
