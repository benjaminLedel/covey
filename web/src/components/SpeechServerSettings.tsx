import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, patch, type SpeechServer } from "../api";

/* The organisation's speech server (#497, #498): an OpenAI-compatible
 * endpoint the control plane asks when a voice names the server as its
 * source, and — only when switched on here — for recognising a call's turns.
 * Without an own server an organisation holding an educa AI token speaks
 * through educa AI. The key goes into the organisation's secrets and is
 * never shown again, only whether one is stored. */
export function SpeechServerSettings({ me }: { me: { Role: string } }) {
  const qc = useQueryClient();
  const darf = me.Role === "org_admin" || me.Role === "agent_owner";
  const cur = useQuery({
    queryKey: ["org-speech-server"],
    queryFn: () => api<SpeechServer>("/org/speech-server"),
    enabled: darf,
    retry: false,
  });
  if (!darf || !cur.data) return null;
  return <Form key={JSON.stringify(cur.data)} value={cur.data} onSaved={() => qc.invalidateQueries({ queryKey: ["org-speech-server"] })} />;
}

function Form({ value, onSaved }: { value: SpeechServer; onSaved: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [base, setBase] = useState(value.base_url);
  const [model, setModel] = useState(value.model);
  const [voice, setVoice] = useState(value.voice);
  const [key, setKey] = useState("");
  const [transcribe, setTranscribe] = useState(value.transcribe);
  const [transcribeModel, setTranscribeModel] = useState(value.transcribe_model);
  const save = useMutation({
    mutationFn: (body: Record<string, unknown>) => patch<SpeechServer>("/org/speech-server", body),
    onSuccess: () => {
      onSaved();
      qc.invalidateQueries({ queryKey: ["speech-voices"] });
    },
  });
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
      ? t("speechServer.inUseOwn", { url: value.effective.base_url })
      : value.effective.source === "educa"
        ? t("speechServer.inUseEduca", { url: value.effective.base_url })
        : t("speechServer.inUseNone");
  return (
    <div className="card mb-4">
      <h2 className="text-sm mb-1" style={{ fontWeight: 600 }}>{t("speechServer.title")}</h2>
      <p className="muted text-xs mt-0 mb-2" style={{ maxWidth: 640 }}>{t("speechServer.hint")}</p>
      <p className="text-xs mt-0 mb-2">{inUse}</p>
      <div className="flex gap-3 flex-wrap items-end">
        {field(t("speechServer.baseUrl"), base, setBase, { placeholder: "https://speech.example.org", style: { minWidth: 280 } })}
        {field(t("speechServer.model"), model, setModel, { maxLength: 100 })}
        {field(t("speechServer.voice"), voice, setVoice, { maxLength: 100, placeholder: "DEFAULT_VOICE" })}
        {field(t("speechServer.key"), key, setKey, {
          type: "password",
          autoComplete: "off",
          placeholder: value.key_set ? t("speechServer.keySet") : t("speechServer.keyNone"),
        })}
      </div>
      <label className="text-xs flex gap-2 items-start mt-3" style={{ maxWidth: 640 }}>
        <input type="checkbox" checked={transcribe} onChange={(e) => setTranscribe(e.target.checked)} />
        <span>
          <span style={{ fontWeight: 600 }}>{t("speechServer.transcribe")}</span>
          <br />
          <span className="muted">{t("speechServer.transcribeHint")}</span>
        </span>
      </label>
      {transcribe && (
        <div className="mt-2">{field(t("speechServer.transcribeModel"), transcribeModel, setTranscribeModel, { maxLength: 100, placeholder: "whisper-1" })}</div>
      )}
      <div className="flex gap-2 items-center mt-2">
        <button className="btn sm" disabled={save.isPending} onClick={() => save.mutate(body(key ? { key } : {}))}>
          {t("speechServer.save")}
        </button>
        {value.key_set && base.trim() !== "" && (
          <button className="btn sm" disabled={save.isPending} onClick={() => save.mutate(body({ key: "" }))}>
            {t("speechServer.removeKey")}
          </button>
        )}
        {save.isSuccess && !save.isPending && <span className="muted text-xs">{t("speechServer.saved")}</span>}
        {save.isError && (
          <span className="text-xs" style={{ color: "var(--error)" }}>
            {(save.error as Error).message}
          </span>
        )}
      </div>
    </div>
  );
}
