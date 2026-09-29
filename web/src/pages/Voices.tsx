import { useQuery } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate, useSearchParams } from "react-router";
import { api, type Principal, type Voice } from "../api";
import { VoiceStepper } from "./voices/VoiceFlow";
import { VoiceMeta, canEditVoices } from "./voices/VoiceHead";
import { flowSteps, nextAction, type Purpose } from "./voices/flow";

/* The voices — a library beside the skills, for the other thing an agent gets
   from its organisation: how to write.

   Why a page of its own rather than a field on the agent: a voice is BUILT from
   uploaded texts or written from a description, it holds for several agents,
   and what a model wrote of it is released by a person. That is an object with
   a life, not a picker (spec/24). Building one is a guided flow of six steps
   (#458, voices/VoiceFlow.tsx).

   The list is only the list (#466): one row per voice with where it stands
   and what it waits for, each leading to the voice's own page. A voice that
   opened inside the list made the page long and nested as soon as there were
   a few. */
export default function Voices({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const voices = useQuery({ queryKey: ["voices"], queryFn: () => api<Voice[]>("/voices"), retry: false });
  const editable = canEditVoices(me.Role);
  const [params, setParams] = useSearchParams();

  // Links from #464 opened a voice in the list (?voice=<id>&step=…); they
  // lead to its page now.
  const legacy = params.get("voice");
  if (legacy) {
    const step = params.get("step");
    return <Navigate to={`/voices/${encodeURIComponent(legacy)}${step ? `?step=${encodeURIComponent(step)}` : ""}`} replace />;
  }

  // The new voice lives in the address (?new=1, then ?new=<id>), so that a
  // reload or Back returns to the step it was on.
  const creating = editable && params.has("new");
  const startNew = () => setParams({ new: "1", step: "purpose" });
  const closeNew = (created?: string) => (created ? navigate(`/voices/${created}`) : setParams({}));

  if (voices.isError) {
    return (
      <div>
        <h1 className="text-[22px] mb-3">{t("voices.title")}</h1>
        <p className="muted">{(voices.error as Error).message}</p>
      </div>
    );
  }
  const list = voices.data ?? [];

  return (
    <div className="vl-page">
      <div className="flex items-baseline gap-3 mb-1 flex-wrap">
        <h1 className="text-[22px]">{t("voices.title")}</h1>
        <span className="muted">{t("voices.subtitle")}</span>
        {editable && !creating && (
          <button className="btn sm primary ml-auto" onClick={startNew}>
            {t("voices.flow.newVoice")}
          </button>
        )}
      </div>
      <p className="muted text-xs mb-4 vl-intro">{t("voices.flow.descShort")}</p>

      {creating && <VoiceStepper onClose={closeNew} />}

      {list.length > 0 && (
        <ul className="card vl-list" aria-label={t("voices.title")}>
          {list.map((v) => (
            <VoiceRow key={v.id} voice={v} editable={editable} />
          ))}
        </ul>
      )}
      {voices.data && list.length === 0 && !creating && (
        <div className="card vl-empty">
          <p className="m-0 font-medium">{t("voices.empty")}</p>
          <p className="muted text-xs mt-1 mb-0">{t("voices.flow.emptyHint")}</p>
          {editable && (
            <button className="btn sm primary mt-3" onClick={startNew}>
              {t("voices.flow.newVoice")}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

/* One voice in the list: its name leads to its page, and the one thing it
   waits for leads straight to that step. */
function VoiceRow({ voice, editable }: { voice: Voice; editable: boolean }) {
  const { t } = useTranslation();
  const steps = flowSteps({ name: voice.name, purpose: (voice.purpose || "") as Purpose, source: voice.source, voice });
  const action = editable ? nextAction(steps) : null;
  return (
    <li className="vl-row">
      <div className="vl-main">
        <div className="vl-title">
          <Link to={`/voices/${voice.id}`} className="vl-name">
            {voice.name}
          </Link>
          {voice.language && <span className="pill mut">{voice.language}</span>}
        </div>
        <VoiceMeta voice={voice} />
      </div>
      {action && (
        <Link className="btn sm vl-action" to={`/voices/${voice.id}?step=${action.step}`}>
          {t(`voices.flow.action.${action.label}`)}
        </Link>
      )}
    </li>
  );
}
