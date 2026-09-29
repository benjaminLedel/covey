import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, Navigate, useNavigate, useParams, useSearchParams } from "react-router";
import { api, del, isNotFound, type Principal, type VoiceCorrection, type VoiceDetail } from "../api";
import { VoiceSteps } from "./voices/VoiceFlow";
import { MoreMenu, VoiceMeta, canEditVoices } from "./voices/VoiceHead";
import { flowSteps, nextAction, voiceStep, type Purpose, type StepKey } from "./voices/flow";

/* A voice's own page (#466): the head that says where it stands, then the flow
   at full width, and beside the flow what people changed and what an agent
   actually gets. Step and tab are in the address (?step=, ?tab=), so a link
   lands where it points and Back walks the steps. */

const TABS = ["flow", "corrections", "tone"] as const;
type Tab = (typeof TABS)[number];

export default function VoicePage({ me }: { me: Principal }) {
  const { t } = useTranslation();
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const editable = canEditVoices(me.Role);
  const [params, setParams] = useSearchParams();
  const detail = useQuery({
    queryKey: ["voice", id],
    queryFn: () => api<VoiceDetail>(`/voices/${id}`),
    retry: (failures, err) => !isNotFound(err) && failures < 1,
  });
  const remove = useMutation({
    mutationFn: () => del(`/voices/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["voices"] });
      navigate("/voices");
    },
  });

  if (isNotFound(detail.error)) return <Navigate to="/voices" replace />;
  if (detail.isError) return <p className="danger-text">{(detail.error as Error).message}</p>;
  if (!detail.data) return <p className="muted text-xs">{t("common.loading")}</p>;
  const v = detail.data;

  const tab: Tab = (TABS as readonly string[]).includes(params.get("tab") ?? "") ? (params.get("tab") as Tab) : "flow";
  const steps = flowSteps({ name: v.name, purpose: (v.purpose || "") as Purpose, source: v.source, voice: v });
  const at = voiceStep(steps, params.get("step"));
  const action = editable ? nextAction(steps) : null;
  // The head's action leads somewhere; on the step it leads to it would only
  // repeat what is open below it.
  const showAction = action && (tab !== "flow" || action.step !== at);

  const go = (next: { tab?: Tab; step?: StepKey }) =>
    setParams((prev) => {
      const n = new URLSearchParams(prev);
      if (next.tab) {
        if (next.tab === "flow") n.delete("tab");
        else n.set("tab", next.tab);
      }
      if (next.step) n.set("step", next.step);
      return n;
    });

  return (
    <div className="vp">
      <div className="text-sm secondary mb-3">
        <Link to="/voices" style={{ color: "inherit" }}>
          {t("voices.title")}
        </Link>{" "}
        / <b className="vp-crumb">{v.name}</b>
      </div>

      <header className="vp-head">
        <div className="vp-title">
          <h1 className="text-[22px]">{v.name}</h1>
          {v.language && <span className="pill mut">{v.language}</span>}
          <div className="vc-actions">
            {showAction && (
              <button className="btn sm primary" onClick={() => go({ tab: "flow", step: action.step })}>
                {t(`voices.flow.action.${action.label}`)}
              </button>
            )}
            {editable && (
              <MoreMenu items={[{ label: t("voices.delete"), danger: true, disabled: remove.isPending, onSelect: () => remove.mutate() }]} />
            )}
          </div>
        </div>
        <VoiceMeta voice={v} />
      </header>

      <div className="vp-tabs" role="tablist" aria-label={v.name}>
        {TABS.map((k) => (
          <button
            key={k}
            type="button"
            role="tab"
            aria-selected={tab === k}
            className={tab === k ? "on" : ""}
            onClick={() => go({ tab: k })}
          >
            {t(`voices.tab.${k}`)}
          </button>
        ))}
      </div>

      <div role="tabpanel" className="card vp-panel">
        {tab === "flow" && <VoiceSteps v={v} editable={editable} at={at} onPick={(k) => go({ step: k })} />}
        {tab === "corrections" && <Corrections id={v.id} editable={editable} />}
        {tab === "tone" && <WhatTheAgentGets v={v} />}
      </div>
    </div>
  );
}

/* What the agent actually gets. The parts on their own do not say what lands
   in the prompt — and that is the question somebody has in front of a voice.
   The contrast stands beside it: it is the measured half of what it gets. */
function WhatTheAgentGets({ v }: { v: VoiceDetail }) {
  const { t } = useTranslation();
  return (
    <div className="vp-section">
      {v.contrast?.length > 0 && (
        <section>
          <h2 className="vp-h">{t("voices.contrast")}</h2>
          <p className="muted vp-lead">{t("voices.contrastHint")}</p>
          <ul className="vp-contrast">
            {v.contrast.map((c) => (
              <li key={c.metric}>
                <span>{c.label || c.metric}</span>
                <span className="muted">{t("voices.contrastValues", { author: c.author, other: c.other })}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
      <section>
        <h2 className="vp-h">{t("voices.tone")}</h2>
        {v.tone.trim() ? <pre className="mono vp-tone">{v.tone}</pre> : <p className="muted m-0">{t("voices.toneEmpty")}</p>}
      </section>
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
    <div className="vp-section">
      <section>
        <h2 className="vp-h">{t("voices.corrections")}</h2>
        <p className="muted vp-lead">{t("voices.correctionsHint")}</p>
        {pairs.data && list.length === 0 && <p className="muted m-0">{t("voices.correctionsEmpty")}</p>}
        <ul className="vp-pairs">
          {list.map((c) => (
            <li key={c.id}>
              <div className="vp-pair-h">
                <span className="muted">
                  {t("voices.correctionFrom", { source: c.action || c.source, when: c.created_at.slice(0, 10) })}
                  {c.agent_slug ? ` · ${c.agent_slug}` : ""}
                </span>
                {editable && (
                  <button className="btn sm ml-auto" onClick={() => drop.mutate(c.id)}>
                    {t("voices.delete")}
                  </button>
                )}
              </div>
              <div className="vp-pair">
                <div>
                  <div className="muted text-xs mb-1">{t("voices.before")}</div>
                  <p className="m-0 vp-was">{c.before}</p>
                </div>
                <div>
                  <div className="muted text-xs mb-1">{t("voices.after")}</div>
                  <p className="m-0">{c.after}</p>
                </div>
              </div>
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
