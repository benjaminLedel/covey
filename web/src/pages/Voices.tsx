import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import { api, del, type Principal, type Voice, type VoiceCorrection, type VoiceDetail } from "../api";
import { VoiceSections, VoiceStepper } from "./voices/VoiceFlow";

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
  const [creating, setCreating] = useState(false);

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
          <button className="btn sm primary ml-auto" onClick={() => setCreating(true)}>
            {t("voices.flow.newVoice")}
          </button>
        )}
      </div>
      <p className="muted text-xs mb-4" style={{ maxWidth: 680 }}>
        {t("voices.flow.desc")}
      </p>

      {editable && creating && <VoiceStepper onClose={() => setCreating(false)} />}

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

function VoiceCard({ voice, editable }: { voice: Voice; editable: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  // A draft an agent filed opens by itself: it is waiting for somebody.
  const [open, setOpen] = useState(!!voice.drafted_by && !voice.released_at);
  const inval = () => {
    qc.invalidateQueries({ queryKey: ["voices"] });
    qc.invalidateQueries({ queryKey: ["voice", voice.id] });
  };
  const remove = useMutation({ mutationFn: () => del(`/voices/${voice.id}`), onSuccess: inval });
  const described = voice.source === "described";
  const pending = (voice.card && voice.card !== voice.released_card) || voice.draft_exemplars?.length > 0;

  return (
    <div className="card mb-3">
      <div className="flex items-baseline gap-2 flex-wrap">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>
          {voice.name}
        </h2>
        {voice.language && <span className="pill mut">{voice.language}</span>}
        {voice.purpose && <span className="pill mut">{t(`voices.flow.purpose.${voice.purpose}`, voice.purpose)}</span>}
        {described ? (
          <span className="pill">{t("voices.flow.describedPill")}</span>
        ) : voice.version > 0 ? (
          <span className="pill ok">{t("voices.version", { version: voice.version })}</span>
        ) : (
          <span className="pill">{t("voices.unbuilt")}</span>
        )}
        {voice.released_at && !pending ? (
          <span className="pill ok">{t("voices.cardReleased")}</span>
        ) : pending ? (
          <span className="pill">{t("voices.flow.draftWaiting")}</span>
        ) : null}
        {voice.drafted_by && (
          <span className="muted text-xs">{t("voices.flow.draftedByShort", { name: voice.drafted_by.display_name })}</span>
        )}
        <span className="muted text-xs ml-auto">
          {voice.agents?.length ? (
            <>
              {t("voices.carriedBy")}{" "}
              {voice.agents.map((a, i) => (
                <span key={a.id}>
                  {i > 0 && ", "}
                  <Link to={`/agents/${a.id}`}>{a.display_name}</Link>
                </span>
              ))}
            </>
          ) : (
            t("voices.nobody")
          )}
        </span>
      </div>

      <p className="muted text-xs mb-2">
        {described ? t("voices.flow.describedNote") : t("voices.builtFrom", { documents: voice.documents, words: voice.words })}
      </p>

      <div className="flex gap-2 mt-2 flex-wrap">
        <button className="btn sm" aria-expanded={open} onClick={() => setOpen(!open)}>
          {open ? t("voices.hide") : t("voices.show")}
        </button>
        {editable && (
          <button className="btn sm danger" disabled={remove.isPending} onClick={() => remove.mutate()}>
            {t("voices.delete")}
          </button>
        )}
      </div>

      {open && <VoiceDetailView id={voice.id} editable={editable} />}
    </div>
  );
}

function VoiceDetailView({ id, editable }: { id: string; editable: boolean }) {
  const { t } = useTranslation();
  const detail = useQuery({ queryKey: ["voice", id], queryFn: () => api<VoiceDetail>(`/voices/${id}`) });

  if (!detail.data) return <p className="muted text-xs mt-3">{t("common.loading")}</p>;
  const v = detail.data;

  return (
    <div className="mt-3 text-xs flex flex-col gap-3">
      <VoiceSections v={v} editable={editable} />

      {v.exemplars?.length > 0 && (
        <div>
          <div className="text-sm font-medium mb-1">{t("voices.exemplars")}</div>
          <p className="muted mb-2" style={{ maxWidth: 680 }}>
            {v.source === "described" ? t("voices.flow.exemplarsDescribed") : t("voices.exemplarsHint")}
          </p>
          {v.exemplars.map((ex, i) => (
            <div key={i} className="mb-2" style={{ maxWidth: 680 }}>
              <span className="pill">{t(`voices.role.${ex.role}`, ex.role)}</span>{" "}
              <span className="muted mono">{ex.from}</span>
              <p className="mt-1 mb-0">{ex.text}</p>
            </div>
          ))}
        </div>
      )}

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
