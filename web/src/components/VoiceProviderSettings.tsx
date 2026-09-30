import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, patch, post, type VoiceProvider, type VoiceProviderTest } from "../api";
import { ProviderVoicePicker } from "./ProviderVoicePicker";

/* The organisation's voice provider (#497, #498): the OpenAI-compatible
 * speech server calls speak through — the one source of covey's voices —
 * and, only when switched on here, where a call's turns are recognised.
 * Without an own server an organisation holding an educa AI token speaks
 * through educa AI. The key goes into the organisation's secrets and is
 * never shown again, only whether one is stored. The test synthesises one
 * sentence with what is saved and says whether it worked. The default voice
 * is chosen from the provider's list where it has one (#518). */
export function VoiceProviderSettings({ me }: { me: { Role: string } }) {
  const qc = useQueryClient();
  const darf = me.Role === "org_admin" || me.Role === "agent_owner";
  const cur = useQuery({
    queryKey: ["org-voice-provider"],
    queryFn: () => api<VoiceProvider>("/org/voice-provider"),
    enabled: darf,
    retry: false,
  });
  if (!darf || !cur.data) return null;
  return (
    <Form key={JSON.stringify(cur.data)} value={cur.data} onSaved={() => qc.invalidateQueries({ queryKey: ["org-voice-provider"] })} />
  );
}

function Form({ value, onSaved }: { value: VoiceProvider; onSaved: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [base, setBase] = useState(value.base_url);
  const [model, setModel] = useState(value.model);
  const [voice, setVoice] = useState(value.voice);
  const [key, setKey] = useState("");
  const [transcribe, setTranscribe] = useState(value.transcribe);
  const [transcribeModel, setTranscribeModel] = useState(value.transcribe_model);
  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch<VoiceProvider>("/org/voice-provider", body),
    onSuccess: () => {
      onSaved();
      qc.invalidateQueries({ queryKey: ["speech-provider"] });
      qc.invalidateQueries({ queryKey: ["provider-voices"] });
      qc.invalidateQueries({ queryKey: ["agent-spoken-voice"] });
    },
  });
  const test = useMutation({ mutationFn: () => post<VoiceProviderTest>("/org/voice-provider/test") });
  const body = (extra: Record<string, unknown> = {}) => ({
    base_url: base,
    model,
    voice,
    transcribe,
    transcribe_model: transcribeModel,
    ...extra,
  });
  const field = (label: string, v: string, set: (s: string) => void, extra: Record<string, unknown> = {}) => (
    <label className="text-xs">
      <div className="muted mb-1">{label}</div>
      <input value={v} onChange={(e) => set(e.target.value)} {...extra} />
    </label>
  );
  const inUse =
    value.effective.source === "own"
      ? t("voiceProvider.inUseOwn", { url: value.effective.base_url })
      : value.effective.source === "educa"
        ? t("voiceProvider.inUseEduca", { url: value.effective.base_url })
        : t("voiceProvider.inUseNone");
  const result = test.data;
  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("voiceProvider.title")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("voiceProvider.hint")}</p>
      <p className="text-xs mt-0 mb-2">{inUse}</p>
      <div className="flex gap-3 flex-wrap items-end">
        {field(t("voiceProvider.baseUrl"), base, setBase, { placeholder: "https://speech.example.org", style: { minWidth: 280 } })}
        {field(t("voiceProvider.model"), model, setModel, { maxLength: 100 })}
        <ProviderVoicePicker
          label={t("voiceProvider.voice")}
          value={voice}
          onChange={setVoice}
          placeholder={t("voiceProvider.voiceDefault")}
          listen
          sampleName="covey"
        />
        {field(t("voiceProvider.key"), key, setKey, {
          type: "password",
          autoComplete: "off",
          placeholder: value.key_set ? t("voiceProvider.keySet") : t("voiceProvider.keyNone"),
        })}
      </div>
      <label className="text-xs flex gap-2 items-start mt-3" style={{ maxWidth: 640 }}>
        <input type="checkbox" checked={transcribe} onChange={(e) => setTranscribe(e.target.checked)} />
        <span>
          <span style={{ fontWeight: 600 }}>{t("voiceProvider.transcribe")}</span>
          <br />
          <span className="muted">{t("voiceProvider.transcribeHint")}</span>
        </span>
      </label>
      {transcribe && (
        <div className="mt-2">
          {field(t("voiceProvider.transcribeModel"), transcribeModel, setTranscribeModel, { maxLength: 100, placeholder: "whisper-1" })}
        </div>
      )}
      <div className="flex gap-2 items-center mt-2 flex-wrap">
        <button className="btn sm" disabled={save.isPending} onClick={() => save.mutate(body(key ? { key } : {}))}>
          {t("voiceProvider.save")}
        </button>
        {value.effective.source !== "none" && (
          <button className="btn sm" disabled={test.isPending} onClick={() => test.mutate()}>
            {test.isPending ? t("voiceProvider.testing") : t("voiceProvider.test")}
          </button>
        )}
        {value.key_set && base.trim() !== "" && (
          <button className="btn sm" disabled={save.isPending} onClick={() => save.mutate(body({ key: "" }))}>
            {t("voiceProvider.removeKey")}
          </button>
        )}
        {save.isSuccess && !save.isPending && <span className="muted text-xs">{t("voiceProvider.saved")}</span>}
        {save.isError && (
          <span className="text-xs" style={{ color: "var(--error)" }}>
            {(save.error as Error).message}
          </span>
        )}
        {result && !test.isPending && (
          <span className="text-xs" style={result.ok ? undefined : { color: "var(--error)" }}>
            {result.ok ? t("voiceProvider.testOk", { ms: result.ms ?? 0 }) : t("voiceProvider.testFailed", { error: result.error ?? "" })}
          </span>
        )}
        {test.isError && (
          <span className="text-xs" style={{ color: "var(--error)" }}>
            {(test.error as Error).message}
          </span>
        )}
      </div>
    </div>
  );
}
