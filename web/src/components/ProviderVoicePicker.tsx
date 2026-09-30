import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "@tanstack/react-query";
import { api, synthesizeSpeech, type ProviderVoice, type ProviderVoiceList } from "../api";

/* A voice at the organisation's voice provider (#518): chosen from the
 * provider's list, grouped by language and shown by the names a person
 * reads, with a button to listen to the chosen one. A provider without a
 * list keeps the free text field. The list is read by the server with the
 * provider's credentials and kept there for an hour. */

export function useProviderVoices() {
  return useQuery({
    queryKey: ["provider-voices"],
    queryFn: () => api<ProviderVoiceList>("/org/voice-provider/voices"),
    retry: false,
    staleTime: 5 * 60_000,
  });
}

/** The language's name in the interface's language; the code where the
 *  browser knows none. */
function languageName(code: string, ui: string) {
  if (!code) return "—";
  try {
    return new Intl.DisplayNames([ui], { type: "language" }).of(code) ?? code;
  } catch {
    return code;
  }
}

/** The voices grouped by language, each group sorted by its name. */
export function groupByLanguage(voices: ProviderVoice[], ui: string) {
  const groups = new Map<string, ProviderVoice[]>();
  for (const v of voices) groups.set(v.language, [...(groups.get(v.language) ?? []), v]);
  return [...groups.entries()]
    .map(([code, vs]) => ({
      code,
      label: languageName(code, ui),
      voices: [...vs].sort((a, b) => a.display_name.localeCompare(b.display_name)),
    }))
    .sort((a, b) => a.label.localeCompare(b.label));
}

export function ProviderVoicePicker({
  label,
  value,
  onChange,
  placeholder,
  readOnly,
  listen,
  sampleName,
  speed,
  instructions,
}: {
  label: string;
  value: string;
  onChange: (name: string) => void;
  /** What an empty choice means, shown as its option or placeholder. */
  placeholder: string;
  readOnly?: boolean;
  /** Offers the Listen button beside the choice. */
  listen?: boolean;
  /** Who introduces themselves in the sample sentence. */
  sampleName: string;
  speed?: number;
  instructions?: string;
}) {
  const { t, i18n } = useTranslation();
  const list = useProviderVoices();
  const listed = !!list.data?.listed && list.data.voices.length > 0;
  const [playing, setPlaying] = useState(false);
  const [error, setError] = useState("");
  const audio = useRef<HTMLAudioElement | null>(null);

  const stop = () => {
    const a = audio.current;
    audio.current = null;
    if (a) {
      a.pause();
      URL.revokeObjectURL(a.src);
    }
    setPlaying(false);
  };
  useEffect(() => stop, []);

  const chosen: ProviderVoice | undefined =
    list.data?.voices.find((v) => v.name === value) ?? (value === "" ? (list.data?.default ?? undefined) : undefined);

  const play = async () => {
    stop();
    setError("");
    setPlaying(true);
    // The sample in the voice's own language where the interface has it: a
    // French voice reading a German sentence says little about it.
    const lng = chosen?.language && i18n.hasResourceBundle(chosen.language, "translation") ? chosen.language : i18n.language;
    try {
      const blob = await synthesizeSpeech({
        text: t("voiceSpeech.sample", { name: sampleName, lng }),
        voice: value || undefined,
        language: chosen?.language || undefined,
        speed,
        instructions,
      });
      const a = new Audio(URL.createObjectURL(blob));
      audio.current = a;
      a.onended = stop;
      await a.play();
    } catch (e) {
      setError((e as Error).message);
      stop();
    }
  };

  if (!listed) {
    return (
      <label className="text-xs">
        <div className="muted mb-1">{label}</div>
        <input value={value} placeholder={placeholder} maxLength={100} readOnly={readOnly} onChange={(e) => onChange(e.target.value)} />
      </label>
    );
  }
  const groups = groupByLanguage(list.data!.voices, i18n.language);
  const unlisted = value !== "" && !list.data!.voices.some((v) => v.name === value);
  return (
    <div className="flex gap-2 items-end flex-wrap">
      <label className="text-xs">
        <div className="muted mb-1">{label}</div>
        <select value={value} disabled={readOnly} onChange={(e) => onChange(e.target.value)}>
          <option value="">{placeholder}</option>
          {unlisted && <option value={value}>{t("voicePicker.unlisted", { name: value })}</option>}
          {groups.map((g) => (
            <optgroup key={g.code} label={g.label}>
              {g.voices.map((v) => (
                <option key={v.name} value={v.name}>
                  {v.display_name}
                </option>
              ))}
            </optgroup>
          ))}
        </select>
      </label>
      {listen && (
        <button className="btn sm" type="button" onClick={playing ? stop : play}>
          {playing ? t("voiceSpeech.stop") : t("voiceSpeech.preview")}
        </button>
      )}
      {error && (
        <span className="text-xs" style={{ color: "var(--error)" }}>
          {error}
        </span>
      )}
    </div>
  );
}
