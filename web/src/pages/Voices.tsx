import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import { api, del, type Principal, type Voice, type VoiceCorrection, type VoiceDetail } from "../api";
import { Avatar } from "../components/person";
import { PurposeIcon, StepMark, VoiceStepper, VoiceSteps } from "./voices/VoiceFlow";
import { flowSteps, isStepKey, nextAction, pendingDraft, voiceStep, type Purpose, type StepKey, type StepStatus } from "./voices/flow";

const canEdit = (role: string) => role === "org_admin" || role === "agent_owner";

/* The voices — a library beside the skills, for the other thing an agent gets
   from its organisation: how to write.

   Why a page of its own rather than a field on the agent: a voice is BUILT from
   uploaded texts or written from a description, it holds for several agents,
   and what a model wrote of it is released by a person. That is an object with
   a life, not a picker (spec/24). Building one is a guided flow of six steps
   (#458, voices/VoiceFlow.tsx). */
export default function Voices({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const voices = useQuery({ queryKey: ["voices"], queryFn: () => api<Voice[]>("/voices"), retry: false });
  const editable = canEdit(me.Role);
  const [params, setParams] = useSearchParams();
  // The new voice lives in the address (?new=1, then ?new=<id>), so that a
  // reload or Back returns to the step it was on.
  const creating = editable && params.has("new");
  const startNew = () => setParams({ new: "1", step: "purpose" });
  const closeNew = () => setParams({});

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
    <div>
      <div className="flex items-baseline gap-3 mb-2 flex-wrap">
        <h1 className="text-[22px]">{t("voices.title")}</h1>
        <span className="muted">{t("voices.subtitle")}</span>
        {editable && !creating && (
          <button className="btn sm primary ml-auto" onClick={startNew}>
            {t("voices.flow.newVoice")}
          </button>
        )}
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("voices.flow.desc")}
      </p>

      {creating && <VoiceStepper onClose={closeNew} />}

      {list.map((v) => (
        <VoiceCard key={v.id} voice={v} editable={editable} />
      ))}
      {voices.data && list.length === 0 && !creating && (
        <div className="card mb-3">
          <p className="m-0">{t("voices.empty")}</p>
          <p className="muted text-xs mt-1 mb-0" style={{ maxWidth: 620 }}>
            {t("voices.flow.emptyHint")}
          </p>
        </div>
      )}
    </div>
  );
}

/* A voice's summary head (#464): what it is, where it stands, who carries it,
   and the one thing it waits for — the rest of its actions in the menu. The
   next action comes from the same rule as the steps (flow.ts), so the head and
   the step list never disagree about what is missing. */
function VoiceCard({ voice, editable }: { voice: Voice; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  // While a new voice is being made, the address is its; a voice below then
  // keeps its step to itself.
  const inURL = !params.has("new") && params.get("voice") === voice.id;
  // A draft an agent filed opens by itself: it is waiting for somebody.
  const [open, setOpen] = useState(inURL || (!!voice.drafted_by && !voice.released_at));
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["voices"] });
    qc.invalidateQueries({ queryKey: ["voice", voice.id] });
  };
  const remove = useMutation({ mutationFn: () => del(`/voices/${voice.id}`), onSuccess: inval });
  const steps = flowSteps({ name: voice.name, purpose: (voice.purpose || "") as Purpose, source: voice.source, voice });
  const action = editable ? nextAction(steps) : null;

  // The step shown: the address's when it names this voice, else the one
  // chosen here, else the first that needs attention. Held once chosen, so a
  // build or a release does not move the page away from what was just done.
  const [picked, setPicked] = useState<StepKey | null>(null);
  const at: StepKey = inURL && isStepKey(params.get("step")) ? (params.get("step") as StepKey) : (picked ?? voiceStep(steps, null));
  const pick = (k: StepKey) => {
    setPicked(k);
    setOpen(true);
    if (!params.has("new")) setParams({ voice: voice.id, step: k });
  };
  const hide = () => {
    setOpen(false);
    setPicked(null);
    if (inURL) setParams({});
  };

  const described = voice.source === "described";
  const pending = pendingDraft(voice);
  const state: StepStatus = voice.released_at && !pending ? "done" : pending ? "open" : "optional";
  const stateLabel =
    state === "done" ? t("voices.cardReleased") : state === "open" ? t("voices.flow.draftWaiting") : t("voices.flow.nothingYet");

  return (
    <div className={`card mb-3 vc${open ? " open" : ""}`}>
      <div className="vc-head">
        <h2 className="vc-title">
          <button type="button" className="vc-name" aria-expanded={open} onClick={() => (open ? hide() : pick(at))}>
            <svg className="vc-chev" viewBox="0 0 16 16" aria-hidden="true">
              <path d="M6 4l4 4-4 4" />
            </svg>
            {voice.name}
          </button>
          {voice.language && <span className="pill mut">{voice.language}</span>}
        </h2>
        <div className="vc-actions">
          {action && (
            <button className="btn sm primary" onClick={() => pick(action.step)}>
              {t(`voices.flow.action.${action.label}`)}
            </button>
          )}
          <MoreMenu
            items={[
              { label: open ? t("voices.hide") : t("voices.show"), onSelect: () => (open ? hide() : pick(at)) },
              ...(editable
                ? [{ label: t("voices.delete"), danger: true, disabled: remove.isPending, onSelect: () => remove.mutate() }]
                : []),
            ]}
          />
        </div>
      </div>

      <ul className="vc-meta">
        <li>
          {voice.purpose ? (
            <>
              <PurposeIcon purpose={voice.purpose} />
              {t(`voices.flow.purpose.${voice.purpose}`, voice.purpose)}
            </>
          ) : (
            <span className="muted">{t("voices.flow.noPurpose")}</span>
          )}
        </li>
        <li>
          {described ? (
            <span className="pill">{t("voices.flow.describedPill")}</span>
          ) : voice.version > 0 ? (
            <span className="pill ok" title={t("voices.builtFrom", { documents: voice.documents, words: voice.words })}>
              {t("voices.flow.measured", { version: voice.version })}
            </span>
          ) : (
            <span className="pill mut">{t("voices.unbuilt")}</span>
          )}
        </li>
        <li>
          <StepMark status={state} />
          {stateLabel}
        </li>
        <li className="vc-carriers">
          <span className="muted">{t("voices.carriedBy")}</span>
          {voice.agents?.length ? (
            voice.agents.map((a) => (
              <Link key={a.id} to={`/agents/${a.id}`} className="vc-agent">
                <Avatar name={a.display_name} slug={a.slug} size={18} />
                {a.display_name}
              </Link>
            ))
          ) : (
            <span className="muted">{t("voices.nobody")}</span>
          )}
        </li>
        {voice.drafted_by && (
          <li className="muted">{t("voices.flow.draftedByShort", { name: voice.drafted_by.display_name })}</li>
        )}
      </ul>

      {open && <VoiceDetailView id={voice.id} editable={editable} at={at} onPick={pick} />}
    </div>
  );
}

/* The menu behind the head's "more" button: the actions a voice has that are
   not what it waits for. */
function MoreMenu({ items }: { items: { label: string; onSelect: () => void; danger?: boolean; disabled?: boolean }[] }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <div className="vc-more" ref={ref}>
      <button type="button" className="vc-more-btn" aria-haspopup="menu" aria-expanded={open} aria-label={t("voices.flow.more")} onClick={() => setOpen(!open)}>
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <circle cx="3.5" cy="8" r="1.25" />
          <circle cx="8" cy="8" r="1.25" />
          <circle cx="12.5" cy="8" r="1.25" />
        </svg>
      </button>
      {open && (
        <div className="vc-menu" role="menu">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              className={it.danger ? "danger" : ""}
              disabled={it.disabled}
              onClick={() => {
                setOpen(false);
                it.onSelect();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function VoiceDetailView({
  id,
  editable,
  at,
  onPick,
}: {
  id: string;
  editable: boolean;
  at: StepKey;
  onPick: (k: StepKey) => void;
}) {
  const { t } = useTranslation();
  const detail = useQuery({ queryKey: ["voice", id], queryFn: () => api<VoiceDetail>(`/voices/${id}`) });

  if (!detail.data) return <p className="muted text-xs mt-3">{t("common.loading")}</p>;
  const v = detail.data;

  return (
    <div className="mt-3 text-xs flex flex-col gap-3">
      <VoiceSteps v={v} editable={editable} at={at} onPick={onPick} />

      {v.contrast?.length > 0 && (
        <div>
          <div className="text-sm font-medium mb-1">{t("voices.contrast")}</div>
          <p className="muted mb-2" style={{ maxWidth: 680 }}>
            {t("voices.contrastHint")}
          </p>
          {v.contrast.map((c) => (
            <div key={c.metric}>
              <span>{c.label || c.metric}</span>{" "}
              <span className="muted">{t("voices.contrastValues", { author: c.author, other: c.other })}</span>
            </div>
          ))}
        </div>
      )}

      <Corrections id={id} editable={editable} />

      {/* What the agent actually gets. The parts on their own do not say
          what lands in the prompt — and that is the question somebody has in
          front of a voice. */}
      <details>
        <summary className="text-sm font-medium">{t("voices.tone")}</summary>
        <pre className="mono" style={{ whiteSpace: "pre-wrap", maxWidth: 680 }}>
          {v.tone}
        </pre>
      </details>
    </div>
  );
}

/* The pairs: what an agent wrote, and what a person made of it.
 *
 * They are listed rather than summarised, because a pair only says something
 * when both halves stand beside each other — and because somebody has to be
 * able to throw out a correction that was made by accident, before it teaches
 * the voice for good. */
function Corrections({ id, editable }: { id: string; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const pairs = useQuery({
    queryKey: ["voice-corrections", id],
    queryFn: () => api<VoiceCorrection[]>(`/voices/${id}/corrections`),
  });
  const drop = useMutation({
    mutationFn: (cid: string) => del(`/voices/${id}/corrections/${cid}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["voice-corrections", id] }),
  });
  const list = pairs.data ?? [];

  return (
    <div>
      <div className="text-sm font-medium mb-1">{t("voices.corrections")}</div>
      <p className="muted mb-2" style={{ maxWidth: 680 }}>
        {t("voices.correctionsHint")}
      </p>
      {list.length === 0 && <p className="muted">{t("voices.correctionsEmpty")}</p>}
      {list.map((c) => (
        <div key={c.id} className="mb-3" style={{ maxWidth: 680 }}>
          <div className="muted mb-1">
            {t("voices.correctionFrom", { source: c.action || c.source, when: c.created_at.slice(0, 10) })}
            {c.agent_slug ? ` · ${c.agent_slug}` : ""}
            {editable && (
              <button className="btn sm ml-2" onClick={() => drop.mutate(c.id)}>
                {t("voices.delete")}
              </button>
            )}
          </div>
          <div className="muted">{t("voices.before")}</div>
          <p className="mt-0 mb-1" style={{ opacity: 0.75 }}>
            {c.before}
          </p>
          <div className="muted">{t("voices.after")}</div>
          <p className="mt-0 mb-0">{c.after}</p>
        </div>
      ))}
    </div>
  );
}
