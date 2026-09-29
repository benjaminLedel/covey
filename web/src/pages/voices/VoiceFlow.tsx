import { useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link } from "react-router";
import {
  api,
  assistStatus,
  del,
  patch,
  post,
  put,
  type ChatTone,
  type Voice,
  type VoiceCheck,
  type VoiceDetail,
  type VoiceExemplar,
} from "../../api";
import { ChatToneForm } from "../../components/ChatToneForm";
import {
  PURPOSES,
  STEP_KEYS,
  blockerFor,
  currentStep,
  flowSteps,
  pendingDraft,
  progress,
  type Purpose,
  type SourceChoice,
  type Step,
  type StepKey,
  type StepStatus,
} from "./flow";

/* The guided way to a voice (#458). One component in two shapes: for a new
   voice a stepper, one step at a time; for an existing one the same six steps
   as sections of its page. Either way the rail on top says what is done and
   the line under it what stands in the way — the rules a corpus has to meet
   used to be written in the specification only, and a person learned them
   from a weak build. */

const USABLE_WORDS = 150;

/* The drawn marks. A state is a shape, never only a colour: a tick is done, a
   ring is open, a dashed ring may be skipped, a bar is held up. */
export function StepMark({ status }: { status: StepStatus }) {
  return (
    <svg className={`vf-mark ${status}`} viewBox="0 0 16 16" aria-hidden="true">
      {status === "done" && <path d="M3.5 8.5l3 3 6-7" />}
      {status === "open" && <circle cx="8" cy="8" r="5" />}
      {status === "optional" && <circle cx="8" cy="8" r="5" strokeDasharray="2.2 2.2" />}
      {status === "blocked" && <path d="M4 8h8" />}
    </svg>
  );
}

function CheckMark({ level }: { level: VoiceCheck["level"] }) {
  return (
    <svg className={`vf-mark check-${level}`} viewBox="0 0 16 16" aria-hidden="true">
      {level === "ok" && <path d="M3.5 8.5l3 3 6-7" />}
      {level === "warn" && <path d="M8 2.8l5.6 10H2.4z M8 6.6v3 M8 11.4v.1" />}
      {level === "block" && <path d="M4 8h8" />}
    </svg>
  );
}

function StepRail({
  steps,
  at,
  onPick,
}: {
  steps: Step[];
  at: StepKey;
  onPick: (k: StepKey) => void;
}) {
  const { t } = useTranslation();
  const { done, total } = progress(steps);
  const blocker = blockerFor(steps, at);
  return (
    <div className="vf-rail-wrap">
      <ol className="vf-rail">
        {steps.map((s, i) => (
          <li key={s.key}>
            <button
              type="button"
              className={s.key === at ? "on" : ""}
              aria-current={s.key === at ? "step" : undefined}
              onClick={() => onPick(s.key)}
            >
              <StepMark status={s.status} />
              <span className="vf-rail-label">
                <span className="vf-rail-n">{i + 1}</span> {t(`voices.flow.step.${s.key}`)}
              </span>
              <span className="sr-only">
                {" — "}
                {t(`voices.flow.status.${s.status}`)}
              </span>
            </button>
          </li>
        ))}
      </ol>
      <p className="vf-status" aria-live="polite">
        <span>{t("voices.flow.progress", { done, total })}</span>
        {blocker && (
          <>
            <span aria-hidden="true"> · </span>
            <span className="vf-blocker">{t(`voices.flow.block.${blocker}`)}</span>
          </>
        )}
      </p>
    </div>
  );
}

function Section({
  id,
  step,
  children,
  hint,
}: {
  id: string;
  step: Step;
  children: ReactNode;
  hint?: ReactNode;
}) {
  const { t } = useTranslation();
  const n = STEP_KEYS.indexOf(step.key) + 1;
  return (
    <section className="vf-section" id={id} aria-labelledby={`${id}-h`}>
      <h3 id={`${id}-h`} className="vf-section-h">
        <StepMark status={step.status} />
        <span className="vf-rail-n">{n}</span> {t(`voices.flow.step.${step.key}`)}
        {!step.required && <span className="muted vf-opt">{t("voices.flow.optional")}</span>}
      </h3>
      {hint && <p className="muted vf-hint">{hint}</p>}
      {children}
    </section>
  );
}

function ErrorLine({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p className="vf-error" role="alert">
      {(error as Error).message}
    </p>
  );
}

function useInvalidate(id?: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["voices"] });
    if (id) qc.invalidateQueries({ queryKey: ["voice", id] });
  };
}

function useModel() {
  const q = useQuery({ queryKey: ["assist-status"], queryFn: assistStatus, retry: false, staleTime: 60_000 });
  return q.data?.available ?? false;
}

/* --- Step bodies ------------------------------------------------------- */

function PurposeFields({
  purpose,
  onPurpose,
  disabled,
}: {
  purpose: Purpose;
  onPurpose: (p: Purpose) => void;
  disabled?: boolean;
}) {
  const { t } = useTranslation();
  return (
    <fieldset className="vf-choices" disabled={disabled}>
      <legend className="sr-only">{t("voices.flow.step.purpose")}</legend>
      {PURPOSES.map((p) => (
        <label key={p} className={`vf-choice ${purpose === p ? "on" : ""}`}>
          <input type="radio" name="vf-purpose" value={p} checked={purpose === p} onChange={() => onPurpose(p)} />
          <span>
            <span className="vf-choice-t">{t(`voices.flow.purpose.${p}`)}</span>
            <span className="muted vf-choice-d">{t(`voices.flow.purposeHint.${p}`)}</span>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

function SourceFields({ source, onSource }: { source: SourceChoice; onSource: (s: SourceChoice) => void }) {
  const { t } = useTranslation();
  return (
    <fieldset className="vf-choices">
      <legend className="sr-only">{t("voices.flow.step.source")}</legend>
      {(["texts", "described", "chat"] as const).map((s) => (
        <label key={s} className={`vf-choice ${source === s ? "on" : ""}`}>
          <input type="radio" name="vf-source" value={s} checked={source === s} onChange={() => onSource(s)} />
          <span>
            <span className="vf-choice-t">{t(`voices.flow.source.${s}`)}</span>
            <span className="muted vf-choice-d">{t(`voices.flow.sourceHint.${s}`)}</span>
          </span>
        </label>
      ))}
    </fieldset>
  );
}

function ChatSource() {
  const { t } = useTranslation();
  return (
    <div className="vf-note">
      <p className="m-0">{t("voices.flow.chat.how")}</p>
      <p className="vf-quote">{t("voices.flow.chat.example")}</p>
      <p className="muted m-0">{t("voices.flow.chat.after")}</p>
      <Link className="btn sm mt-2" to="/team">
        {t("voices.flow.chat.open")}
      </Link>
    </div>
  );
}

function Checks({ checks }: { checks: VoiceCheck[] }) {
  const { t } = useTranslation();
  if (!checks.length) return null;
  return (
    <ul className="vf-checks" aria-label={t("voices.flow.checks")}>
      {checks.map((c) => (
        <li key={c.code}>
          <CheckMark level={c.level} />
          <span>
            {t(`voices.flow.check.${c.code}`, {
              n: c.n ?? 0,
              words: USABLE_WORDS,
              docs: (c.docs ?? []).join(", "),
              detail: c.detail ? t(`voices.flow.register.${c.detail}`, c.detail) : "",
              lang: c.detail ?? "",
            })}
          </span>
        </li>
      ))}
    </ul>
  );
}

function TextsMaterial({ v, editable }: { v: VoiceDetail; editable: boolean }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const [name, setName] = useState("");
  const [body, setBody] = useState("");
  const [kind, setKind] = useState<"author" | "reference">("author");
  const fileRef = useRef<HTMLInputElement>(null);
  const add = useMutation({
    mutationFn: (d: { name: string; body: string; kind: string }) => post(`/voices/${v.id}/documents`, d),
    onSuccess: () => {
      setName("");
      setBody("");
      inval();
    },
  });
  const drop = useMutation({ mutationFn: (docID: string) => del(`/voices/${v.id}/documents/${docID}`), onSuccess: inval });
  const build = useMutation({ mutationFn: () => post<Voice>(`/voices/${v.id}/build`), onSuccess: inval });
  const authors = v.corpus.filter((d) => d.kind === "author");

  // Files are read here and sent as text: the corpus is prose, and the
  // server measures what it gets rather than guessing at a format.
  const onFiles = async (files: FileList | null) => {
    for (const f of Array.from(files ?? [])) {
      const text = await f.text();
      add.mutate({ name: f.name, body: text, kind });
    }
    if (fileRef.current) fileRef.current.value = "";
  };

  return (
    <div className="vf-body">
      <Checks checks={v.checks} />
      {v.corpus.length > 0 && (
        <ul className="vf-docs">
          {v.corpus.map((d) => (
            <li key={d.id}>
              <span className="mono vf-doc-name">{d.name}</span>
              <span className="muted">{t("voices.documentWords", { words: d.words })}</span>
              {d.kind === "reference" && <span className="pill mut">{t("voices.reference")}</span>}
              {d.kind === "author" && d.words < USABLE_WORDS && (
                <span className="pill mut">{t("voices.flow.noBand")}</span>
              )}
              {editable && (
                <button className="btn sm ml-auto" onClick={() => drop.mutate(d.id)}>
                  {t("voices.delete")}
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {editable && (
        <div className="vf-upload">
          <div className="flex gap-2 items-center flex-wrap">
            <select value={kind} onChange={(e) => setKind(e.target.value as "author" | "reference")} aria-label={t("voices.flow.kind")}>
              <option value="author">{t("voices.kindAuthor")}</option>
              <option value="reference">{t("voices.kindReference")}</option>
            </select>
            <label className="btn sm">
              {t("voices.flow.chooseFiles")}
              <input
                ref={fileRef}
                type="file"
                className="sr-only"
                multiple
                accept=".md,.txt,.markdown,.html,text/plain,text/markdown"
                onChange={(e) => onFiles(e.target.files)}
              />
            </label>
            <span className="muted text-xs">{t("voices.flow.orPaste")}</span>
          </div>
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("voices.documentName")} />
          <textarea rows={5} value={body} onChange={(e) => setBody(e.target.value)} placeholder={t("voices.documentBody")} />
          <div className="flex gap-2 items-center flex-wrap">
            <button
              className="btn sm"
              disabled={!name.trim() || !body.trim() || add.isPending}
              onClick={() => add.mutate({ name, body, kind })}
            >
              {t("voices.addDocument")}
            </button>
            <span className="muted text-xs">{t("voices.flow.corpusRule")}</span>
          </div>
          <ErrorLine error={add.error} />
        </div>
      )}
      {editable && (
        <div className="flex gap-2 items-center flex-wrap">
          <button
            className="btn sm primary"
            disabled={authors.length === 0 || build.isPending}
            onClick={() => build.mutate()}
          >
            {build.isPending ? t("voices.building") : v.version > 0 ? t("voices.flow.rebuild") : t("voices.build")}
          </button>
          <span className="muted text-xs">{t("voices.flow.buildCost")}</span>
        </div>
      )}
      <ErrorLine error={build.error} />
      {v.notes?.map((n, i) => (
        <p key={i} className="vf-buildnote">
          {n}
        </p>
      ))}
    </div>
  );
}

function DescriptionMaterial({ v, editable }: { v: VoiceDetail; editable: boolean }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const model = useModel();
  const [text, setText] = useState(v.description ?? "");
  const describe = useMutation({
    mutationFn: () => post<Voice>(`/voices/${v.id}/describe`, { description: text, language: v.language }),
    onSuccess: inval,
  });
  const written = (v.card ?? "").trim() !== "" || (v.draft_exemplars?.length ?? 0) > 0 || v.exemplars.length > 0;
  return (
    <div className="vf-body">
      <div className="vf-guide">
        <p className="m-0">{t("voices.flow.describe.lead")}</p>
        <ul>
          {(["open", "facts", "sentences", "never", "address"] as const).map((k) => (
            <li key={k}>{t(`voices.flow.describe.ask.${k}`)}</li>
          ))}
        </ul>
      </div>
      <label className="vf-field">
        <span className="muted text-xs">{t("voices.flow.describe.label")}</span>
        <textarea
          rows={7}
          value={text}
          maxLength={4000}
          readOnly={!editable}
          onChange={(e) => setText(e.target.value)}
          placeholder={t("voices.flow.describe.placeholder")}
        />
      </label>
      {editable && (
        <div className="flex gap-2 items-center flex-wrap">
          <button
            className="btn sm primary"
            disabled={!model || text.trim().length < 20 || describe.isPending}
            onClick={() => describe.mutate()}
          >
            {describe.isPending
              ? t("voices.flow.describe.writing")
              : written
                ? t("voices.flow.describe.again")
                : t("voices.flow.describe.write")}
          </button>
          {!text.trim() && (
            <button className="btn sm" onClick={() => setText(t("voices.flow.describe.example"))}>
              {t("voices.flow.describe.useExample")}
            </button>
          )}
          <span className="muted text-xs">{model ? t("voices.flow.describe.cost") : t("voices.flow.noModel")}</span>
        </div>
      )}
      <ErrorLine error={describe.error} />
    </div>
  );
}

function ToneStep({ v, editable }: { v: VoiceDetail; editable: boolean }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const save = useMutation({ mutationFn: (tone: ChatTone) => put<Voice>(`/voices/${v.id}/chat-tone`, tone), onSuccess: inval });
  const own = v.chat_tone && Object.values(v.chat_tone).some((x) => x);
  const suggested = v.suggested_chat_tone && Object.values(v.suggested_chat_tone).some((x) => x);
  const value = own ? v.chat_tone! : suggested ? v.suggested_chat_tone! : {};
  return (
    <div className="vf-body">
      {!own && suggested && <p className="vf-note m-0">{t("voices.flow.toneSuggested")}</p>}
      <ChatToneForm
        key={JSON.stringify(value)}
        value={value}
        editable={editable}
        saving={save.isPending}
        error={save.isError ? (save.error as Error).message : undefined}
        onSave={(tone) => save.mutate(tone)}
      />
    </div>
  );
}

type Sample = { text: string; kind: string; version: string };

function PreviewStep({ v, editable, onHeard }: { v: VoiceDetail; editable: boolean; onHeard: () => void }) {
  const { t } = useTranslation();
  const model = useModel();
  const [topic, setTopic] = useState("");
  const [kind, setKind] = useState("");
  const [samples, setSamples] = useState<Sample[]>([]);
  const both = !!v.released_card && pendingDraft(v);
  const [version, setVersion] = useState<"draft" | "released">("draft");
  const write = useMutation({
    mutationFn: () => post<Sample>(`/voices/${v.id}/preview`, { topic, kind, version: both ? version : "draft" }),
    onSuccess: (s) => {
      // Two at most: enough to hear the voice twice, or before beside after.
      setSamples((prev) => [s, ...prev].slice(0, 2));
      onHeard();
    },
  });
  if (!editable) return <p className="muted m-0">{t("voices.flow.previewReadOnly")}</p>;
  return (
    <div className="vf-body">
      <div className="vf-preview-form">
        <label className="vf-field grow">
          <span className="muted text-xs">{t("voices.flow.topic")}</span>
          <input value={topic} maxLength={300} onChange={(e) => setTopic(e.target.value)} placeholder={t("voices.flow.topicPlaceholder")} />
        </label>
        <label className="vf-field">
          <span className="muted text-xs">{t("voices.flow.kind")}</span>
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            <option value="">{t("voices.flow.kindDefault")}</option>
            {(["paragraph", "mail", "chat"] as const).map((k) => (
              <option key={k} value={k}>
                {t(`voices.flow.kinds.${k}`)}
              </option>
            ))}
          </select>
        </label>
        {both && (
          <label className="vf-field">
            <span className="muted text-xs">{t("voices.flow.hear")}</span>
            <select value={version} onChange={(e) => setVersion(e.target.value as "draft" | "released")}>
              <option value="draft">{t("voices.flow.versionDraft")}</option>
              <option value="released">{t("voices.flow.versionReleased")}</option>
            </select>
          </label>
        )}
      </div>
      <div className="flex gap-2 items-center flex-wrap">
        <button className="btn sm primary" disabled={!model || !topic.trim() || write.isPending} onClick={() => write.mutate()}>
          {write.isPending ? t("voices.flow.writingSample") : samples.length ? t("voices.flow.again") : t("voices.flow.writeSample")}
        </button>
        <span className="muted text-xs">{model ? t("voices.flow.previewCost") : t("voices.flow.noModel")}</span>
      </div>
      <ErrorLine error={write.error} />
      {samples.map((s, i) => (
        <figure key={samples.length - i} className="vf-sample">
          <figcaption className="muted text-xs">
            {t(`voices.flow.kinds.${s.kind}`, s.kind)} · {s.version === "released" ? t("voices.flow.versionReleased") : t("voices.flow.versionDraft")}
          </figcaption>
          <p className="m-0">{s.text}</p>
        </figure>
      ))}
    </div>
  );
}

function ExemplarList({ list }: { list: VoiceExemplar[] }) {
  const { t } = useTranslation();
  return (
    <div className="vf-exemplars">
      {list.map((ex, i) => (
        <div key={i}>
          <span className="pill mut">{t(`voices.role.${ex.role}`, ex.role)}</span>
          {ex.from && <span className="muted mono text-xs"> {ex.from}</span>}
          <p className="mt-1 mb-0">{ex.text}</p>
        </div>
      ))}
    </div>
  );
}

function ReleaseStep({ v, editable }: { v: VoiceDetail; editable: boolean }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const model = useModel();
  const [card, setCard] = useState<string | null>(null);
  const [instruction, setInstruction] = useState("");
  const [before, setBefore] = useState<string | null>(null);
  const draft = card ?? (v.card || v.released_card);
  const release = useMutation({
    mutationFn: () => post(`/voices/${v.id}/release`, { card: card ?? draft }),
    onSuccess: () => {
      setCard(null);
      setBefore(null);
      inval();
    },
  });
  const refine = useMutation({
    mutationFn: () => post<{ voice: Voice; before: { card: string } }>(`/voices/${v.id}/refine`, { instruction }),
    onSuccess: (r) => {
      setBefore(r.before.card);
      setCard(null);
      setInstruction("");
      inval();
    },
  });
  const pending = pendingDraft(v) || card !== null;
  const described = v.source === "described";

  if (!draft) return <p className="muted m-0">{t("voices.cardMissing")}</p>;
  return (
    <div className="vf-body">
      <p className="muted m-0">{described ? t("voices.flow.releaseDescribed") : t("voices.cardHint")}</p>
      {before !== null && (
        <div className="vf-compare">
          <div>
            <div className="muted text-xs mb-1">{t("voices.flow.before")}</div>
            <div className="vf-card-text was">{before}</div>
          </div>
          <div>
            <div className="muted text-xs mb-1">{t("voices.flow.after")}</div>
            <div className="vf-card-text">{v.card}</div>
          </div>
        </div>
      )}
      <label className="vf-field">
        <span className="muted text-xs">{pending ? t("voices.flow.cardDraft") : t("voices.flow.cardReleased")}</span>
        <textarea rows={9} value={draft} onChange={(e) => setCard(e.target.value)} readOnly={!editable} />
      </label>
      {v.draft_exemplars?.length > 0 && (
        <details className="vf-details" open={described && !v.released_at}>
          <summary>{t("voices.flow.draftExemplars", { n: v.draft_exemplars.length })}</summary>
          <ExemplarList list={v.draft_exemplars} />
        </details>
      )}
      {editable && (
        <div className="vf-refine">
          <label className="vf-field grow">
            <span className="muted text-xs">{t("voices.flow.refineLabel")}</span>
            <input
              value={instruction}
              maxLength={1000}
              onChange={(e) => setInstruction(e.target.value)}
              placeholder={t("voices.flow.refinePlaceholder")}
            />
          </label>
          <button className="btn sm" disabled={!model || !instruction.trim() || refine.isPending} onClick={() => refine.mutate()}>
            {refine.isPending ? t("voices.flow.refining") : t("voices.flow.refine")}
          </button>
        </div>
      )}
      {editable && !model && <p className="muted text-xs m-0">{t("voices.flow.noModel")}</p>}
      <ErrorLine error={refine.error} />
      {editable && (
        <div className="flex gap-2 items-center flex-wrap">
          <button className="btn sm primary" disabled={!pending || release.isPending} onClick={() => release.mutate()}>
            {t("voices.release")}
          </button>
          <span className="muted text-xs">
            {pending
              ? t("voices.flow.releaseEffect")
              : v.released_at
                ? t("voices.releasedAt", { at: v.released_at.slice(0, 10) })
                : ""}
          </span>
        </div>
      )}
      <ErrorLine error={release.error} />
    </div>
  );
}

/* --- The two shapes ---------------------------------------------------- */

/** An existing voice: the six steps as sections of its page. */
export function VoiceSections({ v, editable }: { v: VoiceDetail; editable: boolean }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const [previewed, setPreviewed] = useState(false);
  const setPurpose = useMutation({
    mutationFn: (purpose: string) => patch<Voice>(`/voices/${v.id}`, { purpose }),
    onSuccess: inval,
  });
  const steps = flowSteps({
    name: v.name,
    purpose: (v.purpose || "") as Purpose,
    source: v.source,
    voice: v,
    previewed,
  });
  const at = currentStep(steps);
  const step = (k: StepKey) => steps.find((s) => s.key === k)!;
  const anchor = (k: StepKey) => `vf-${v.id}-${k}`;
  const described = v.source === "described";

  return (
    <div className="vf">
      <StepRail
        steps={steps}
        at={at}
        onPick={(k) => document.getElementById(anchor(k))?.scrollIntoView({ behavior: "smooth", block: "start" })}
      />
      <Section id={anchor("purpose")} step={step("purpose")} hint={t("voices.flow.purposeLead")}>
        <PurposeFields
          purpose={(v.purpose || "") as Purpose}
          onPurpose={(p) => setPurpose.mutate(p)}
          disabled={!editable || setPurpose.isPending}
        />
        <ErrorLine error={setPurpose.error} />
      </Section>
      <Section id={anchor("source")} step={step("source")}>
        <p className="m-0">
          {described ? t("voices.flow.isDescribed") : t("voices.flow.isMeasured", { documents: v.documents, words: v.words })}
        </p>
        {v.drafted_by && (
          <p className="muted m-0">
            {t("voices.flow.draftedBy")} <Link to={`/agents/${v.drafted_by.id}`}>{v.drafted_by.display_name}</Link>
          </p>
        )}
      </Section>
      <Section id={anchor("material")} step={step("material")}>
        {described ? (
          <>
            <DescriptionMaterial v={v} editable={editable} />
            <details className="vf-details">
              <summary>{t("voices.flow.measureIt")}</summary>
              <p className="muted">{t("voices.flow.measureItHint")}</p>
              <TextsMaterial v={v} editable={editable} />
            </details>
          </>
        ) : (
          <TextsMaterial v={v} editable={editable} />
        )}
      </Section>
      <Section id={anchor("tone")} step={step("tone")} hint={t("chatTone.hintVoice")}>
        <ToneStep v={v} editable={editable} />
      </Section>
      <Section id={anchor("preview")} step={step("preview")} hint={t("voices.flow.previewLead")}>
        {step("preview").status === "blocked" ? (
          <p className="muted m-0">{t("voices.flow.block.nothingToHear")}</p>
        ) : (
          <PreviewStep v={v} editable={editable} onHeard={() => setPreviewed(true)} />
        )}
      </Section>
      <Section id={anchor("release")} step={step("release")}>
        <ReleaseStep v={v} editable={editable} />
      </Section>
    </div>
  );
}

/** A new voice: the same six steps, one at a time. */
export function VoiceStepper({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [name, setName] = useState("");
  const [language, setLanguage] = useState("de");
  const [purpose, setPurpose] = useState<Purpose>("");
  const [source, setSource] = useState<SourceChoice>("");
  const [id, setId] = useState<string | null>(null);
  const [at, setAt] = useState<StepKey>("purpose");
  const [previewed, setPreviewed] = useState(false);
  const detail = useQuery({
    queryKey: ["voice", id],
    queryFn: () => api<VoiceDetail>(`/voices/${id}`),
    enabled: !!id,
  });
  const v = id ? detail.data : undefined;
  const create = useMutation({
    mutationFn: () => post<Voice>("/voices", { name, language, purpose }),
    onSuccess: (created) => {
      setId(created.id);
      qc.invalidateQueries({ queryKey: ["voices"] });
      setAt("material");
    },
  });
  const steps = flowSteps({ name, purpose, source, voice: v, previewed });
  const i = STEP_KEYS.indexOf(at);
  const blocker = blockerFor(steps, at);
  const creates = at === "source" && blocker === "create";
  const last = at === "release";
  const canNext = !last && (creates ? !create.isPending : blocker === null);
  const next = () => (creates ? create.mutate() : setAt(STEP_KEYS[i + 1]));
  // Once the voice exists, the first two steps are its own: they are
  // changed on its page, not by walking back.
  const pick = (k: StepKey) => {
    if (id && (k === "purpose" || k === "source")) return;
    const target = STEP_KEYS.indexOf(k);
    const open = STEP_KEYS.slice(0, target).every((key) => blockerFor(steps, key) === null || (key === "source" && !!id));
    if (target <= i || open) setAt(k);
  };

  let body: ReactNode = null;
  if (at === "purpose") {
    body = (
      <>
        <div className="vf-name">
          <label className="vf-field grow">
            <span className="muted text-xs">{t("voices.name")}</span>
            <input value={name} disabled={!!id} onChange={(e) => setName(e.target.value)} placeholder={t("voices.namePlaceholder")} />
          </label>
          <label className="vf-field">
            <span className="muted text-xs">{t("voices.language")}</span>
            <input className="mono" style={{ width: 70 }} value={language} disabled={!!id} onChange={(e) => setLanguage(e.target.value)} />
          </label>
        </div>
        <p className="muted vf-hint">{t("voices.flow.purposeLead")}</p>
        <PurposeFields purpose={purpose} onPurpose={setPurpose} disabled={!!id} />
      </>
    );
  } else if (at === "source") {
    body = (
      <>
        <SourceFields source={source} onSource={setSource} />
        {source === "chat" && <ChatSource />}
        <ErrorLine error={create.error} />
      </>
    );
  } else if (source === "chat") {
    body = <ChatSource />;
  } else if (!v) {
    body = <p className="muted">{t("common.loading")}</p>;
  } else if (at === "material") {
    body = source === "described" ? <DescriptionMaterial v={v} editable /> : <TextsMaterial v={v} editable />;
  } else if (at === "tone") {
    body = (
      <>
        <p className="muted vf-hint">{t("chatTone.hintVoice")}</p>
        <ToneStep v={v} editable />
      </>
    );
  } else if (at === "preview") {
    body = (
      <>
        <p className="muted vf-hint">{t("voices.flow.previewLead")}</p>
        {steps[4].status === "blocked" ? (
          <p className="muted">{t("voices.flow.block.nothingToHear")}</p>
        ) : (
          <PreviewStep v={v} editable onHeard={() => setPreviewed(true)} />
        )}
      </>
    );
  } else {
    body = <ReleaseStep v={v} editable />;
  }

  return (
    <div className="card mb-4 vf vf-new">
      <div className="flex items-baseline gap-2 mb-2">
        <h2 className="text-sm" style={{ fontWeight: 600 }}>
          {id && v ? v.name : t("voices.flow.newTitle")}
        </h2>
        <button className="btn sm ml-auto" onClick={onClose}>
          {id ? t("voices.flow.done") : t("voices.flow.cancel")}
        </button>
      </div>
      <StepRail steps={steps} at={at} onPick={pick} />
      <div className="vf-stage">
        <h3 className="vf-section-h">
          <span className="vf-rail-n">{i + 1}</span> {t(`voices.flow.step.${at}`)}
          {!steps[i].required && <span className="muted vf-opt">{t("voices.flow.optional")}</span>}
        </h3>
        {body}
      </div>
      <div className="vf-nav">
        <button className="btn sm" disabled={i === 0 || (!!id && i <= 2)} onClick={() => setAt(STEP_KEYS[i - 1])}>
          {t("voices.flow.back")}
        </button>
        {!last && (
          <button className="btn sm primary" disabled={!canNext} onClick={next}>
            {creates ? t("voices.flow.createAndContinue") : t("voices.flow.next")}
          </button>
        )}
        {last && (
          <button className="btn sm primary" onClick={onClose}>
            {t("voices.flow.done")}
          </button>
        )}
      </div>
    </div>
  );
}
