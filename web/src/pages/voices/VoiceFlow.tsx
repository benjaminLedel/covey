import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { Link, useSearchParams } from "react-router";
import {
  api,
  assistStatus,
  del,
  patch,
  post,
  put,
  type ChatTone,
  type VoiceSpeech,
  type Department,
  type Voice,
  type VoiceCheck,
  type VoiceDetail,
  type VoiceExemplar,
} from "../../api";
import { ChatToneForm } from "../../components/ChatToneForm";
import { SpeechForm } from "../../components/SpeechForm";
import { Markdown } from "../../components/Markdown";
import {
  PURPOSES,
  STEP_KEYS,
  blockerFor,
  flowSteps,
  pendingDraft,
  progress,
  reachable,
  shownStatus,
  stepperStep,
  type ShownStatus,
  type Purpose,
  type SourceChoice,
  type Step,
  type StepKey,
} from "./flow";

/* The guided way to a voice (#458). One frame in two uses: for a new voice a
   stepper whose Next waits until the step lets the flow through; for an
   existing one the same six steps as its navigation. Either way one step is
   shown at a time (#464), and the list beside it says what is done and what
   stands in the way — the rules a corpus has to meet used to be written in the
   specification only, and a person learned them from a weak build. */

const USABLE_WORDS = 150;

/* The drawn marks. A state is a shape, never only a colour: a tick is done, a
   ring is open, a dashed ring may be skipped, a bar is held up, a dot holds a
   value that was set before the steps above it were walked (#466). */
export function StepMark({ status }: { status: ShownStatus }) {
  return (
    <svg className={`vf-mark ${status}`} viewBox="0 0 16 16" aria-hidden="true">
      {status === "done" && <path d="M3.5 8.5l3 3 6-7" />}
      {status === "open" && <circle cx="8" cy="8" r="5" />}
      {status === "optional" && <circle cx="8" cy="8" r="5" strokeDasharray="2.2 2.2" />}
      {status === "blocked" && <path d="M4 8h8" />}
      {status === "preset" && <circle cx="8" cy="8" r="2.2" />}
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

/* Drawn, like the marks: one stroke weight, a 24-unit grid, currentColor. */
const PURPOSE_ICON: Record<Exclude<Purpose, "">, ReactNode> = {
  blog: (
    <>
      <path d="M6 3.5h8l4 4v13H6z" />
      <path d="M14 3.5v4h4M9 12h6M9 15.5h6" />
    </>
  ),
  support_mail: (
    <>
      <rect x="3.5" y="5.5" width="17" height="13" rx="1.5" />
      <path d="M4 7l8 6 8-6" />
    </>
  ),
  chat: (
    <>
      <path d="M20 12a7.5 7.5 0 0 1-7.5 7.5H8l-4 3v-4.3A7.5 7.5 0 1 1 20 12z" />
      <path d="M8.5 11h7M8.5 14.5h4" />
    </>
  ),
  offers: (
    <>
      <path d="M6 3.5h12v17H6z" />
      <path d="M9 8h6M9 11h6M9 16.5c.8-1.4 1.7-1.4 2.3 0s1.5 1.4 2.3 0 1.4-1 1.9.2" />
    </>
  ),
  other: (
    <>
      <path d="M4.5 19.5l1-4L15.8 5.2a1.8 1.8 0 0 1 2.5 0l.5.5a1.8 1.8 0 0 1 0 2.5L8.5 18.5z" />
      <path d="M14 7l3 3" />
    </>
  ),
};

export function PurposeIcon({ purpose }: { purpose: string }) {
  const icon = PURPOSE_ICON[purpose as Exclude<Purpose, "">];
  if (!icon) return null;
  return (
    <svg className="vf-icon" viewBox="0 0 24 24" aria-hidden="true">
      {icon}
    </svg>
  );
}

/** What a step says under its title: what stands in the way, or its state.
 *  The chosen step only says its state — what holds it up is said once, at
 *  Next (#466). */
function stepLine(t: (k: string) => string, s: Step, shown: ShownStatus, current: boolean): string {
  if (!current && s.blocker && s.status !== "done") return t(`voices.flow.block.${s.blocker}`);
  return t(`voices.flow.status.${shown}`);
}

/* The frame both shapes share (#464): the six steps as a list that stays in
   view — a bar above the step once the flow is narrow — and beside it the one
   step that is chosen, with Back and Next under it. Narrow and wide are the
   flow's own width (a container query), not the window's: the admin shell
   keeps its sidebar at any width. */
function FlowFrame({
  steps,
  at,
  onPick,
  canPick,
  hint,
  nav,
  children,
}: {
  steps: Step[];
  at: StepKey;
  onPick: (k: StepKey) => void;
  canPick: (k: StepKey) => boolean;
  hint?: ReactNode;
  nav: ReactNode;
  children: ReactNode;
}) {
  const { t } = useTranslation();
  const { done, total } = progress(steps);
  const i = STEP_KEYS.indexOf(at);
  const step = steps[i];
  const listRef = useRef<HTMLOListElement>(null);
  const headingId = useId();

  // In the bar the chosen step may sit off to the side: bring it in, without
  // scrolling the page (scrollIntoView would).
  useEffect(() => {
    const ol = listRef.current;
    const on = ol?.querySelector<HTMLElement>("[aria-current]");
    if (!ol || !on || ol.scrollWidth <= ol.clientWidth) return;
    const left = on.offsetLeft - 12;
    const right = on.offsetLeft + on.offsetWidth + 12 - ol.clientWidth;
    if (ol.scrollLeft > left) ol.scrollLeft = left;
    else if (ol.scrollLeft < right) ol.scrollLeft = right;
  }, [at]);

  return (
    <div className="vf-frame">
      <nav className="vf-steps" aria-label={t("voices.flow.stepsNav")}>
        <p className="vf-progress">{t("voices.flow.progress", { done, total })}</p>
        <ol ref={listRef}>
          {steps.map((s, n) => (
            <li key={s.key}>
              <button
                type="button"
                className={s.key === at ? "on" : ""}
                aria-current={s.key === at ? "step" : undefined}
                disabled={s.key !== at && !canPick(s.key)}
                onClick={() => onPick(s.key)}
              >
                <StepMark status={shownStatus(steps, s.key)} />
                <span className="vf-step-text">
                  <span className="vf-step-t">
                    <span className="vf-rail-n">{n + 1}</span> {t(`voices.flow.step.${s.key}`)}
                  </span>
                  <span className="vf-step-s">{stepLine(t, s, shownStatus(steps, s.key), s.key === at)}</span>
                </span>
                <span className="sr-only">
                  {" — "}
                  {t(`voices.flow.status.${shownStatus(steps, s.key)}`)}
                </span>
              </button>
            </li>
          ))}
        </ol>
      </nav>
      <section className="vf-pane" aria-labelledby={headingId}>
        <header className="vf-pane-h">
          <h3 id={headingId} className="vf-section-h">
            <StepMark status={shownStatus(steps, at)} />
            {t(`voices.flow.step.${at}`)}
            {!step.required && <span className="muted vf-opt">{t("voices.flow.optional")}</span>}
          </h3>
          <span className="muted vf-stepof">{t("voices.flow.stepOf", { n: i + 1, total })}</span>
        </header>
        {hint && <p className="muted vf-hint">{hint}</p>}
        <div className="vf-pane-body">{children}</div>
        <div className="vf-nav">{nav}</div>
      </section>
    </div>
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

/* The purpose as one compact choice (#464): five labels with their icon, and
   the sentence that explains the chosen one under the row — the others carry
   theirs as a tooltip. */
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
  const name = useId();
  return (
    <fieldset className="vf-seg-wrap" disabled={disabled}>
      <legend className="sr-only">{t("voices.flow.step.purpose")}</legend>
      <div className="vf-seg">
        {PURPOSES.map((p) => (
          <label key={p} className={purpose === p ? "on" : ""} title={t(`voices.flow.purposeHint.${p}`)}>
            <input type="radio" name={name} value={p} checked={purpose === p} onChange={() => onPurpose(p)} />
            <PurposeIcon purpose={p} />
            <span>{t(`voices.flow.purpose.${p}`)}</span>
          </label>
        ))}
      </div>
      {purpose && <p className="muted vf-seg-desc">{t(`voices.flow.purposeHint.${purpose}`)}</p>}
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
      {/* "No texts" is the step's blocker, said at Next. */}
      <Checks checks={v.checks.filter((c) => c.code !== "no_texts")} />
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
  const saveSpeech = useMutation({
    mutationFn: (sp: VoiceSpeech | Record<string, never>) => put<Voice>(`/voices/${v.id}/speech`, sp),
    onSuccess: inval,
  });
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
      {/* The spoken voice (#497): how the same agents sound in a call. */}
      <h4 className="m-0 mt-3">{t("voiceSpeech.title")}</h4>
      <p className="vf-note m-0">{t("voiceSpeech.hint")}</p>
      <SpeechForm
        key={JSON.stringify(v.speech ?? null)}
        value={v.speech}
        editable={editable}
        saving={saveSpeech.isPending}
        error={saveSpeech.isError ? (saveSpeech.error as Error).message : undefined}
        onSave={(sp) => saveSpeech.mutate(sp)}
      />
    </div>
  );
}

type Sample = { text: string; kind: string; version: string; audience?: string };

function PreviewStep({ v, editable, onHeard }: { v: VoiceDetail; editable: boolean; onHeard: () => void }) {
  const { t } = useTranslation();
  const model = useModel();
  const [topic, setTopic] = useState("");
  const [kind, setKind] = useState("");
  const [samples, setSamples] = useState<Sample[]>([]);
  const both = !!v.released_card && pendingDraft(v);
  const [version, setVersion] = useState<"draft" | "released">("draft");
  // Written to somebody of a department (#471): the sample then carries that
  // department's "how to speak with us" line, as a turn to them would.
  const [audience, setAudience] = useState("");
  const departments = useQuery({
    queryKey: ["departments"],
    queryFn: () => api<Department[] | null>("/departments"),
    enabled: editable,
  });
  const depts = departments.data ?? [];
  const write = useMutation({
    mutationFn: () =>
      post<Sample>(`/voices/${v.id}/preview`, {
        topic,
        kind,
        version: both ? version : "draft",
        ...(audience ? { audience } : {}),
      }).then((s) => ({ ...s, audience: depts.find((d) => d.id === audience)?.name ?? "" })),
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
        {depts.length > 0 && (
          <label className="vf-field">
            <span className="muted text-xs">{t("voices.occ.previewFor")}</span>
            <select value={audience} onChange={(e) => setAudience(e.target.value)}>
              <option value="">{t("voices.occ.previewForNone")}</option>
              {depts.map((d) => (
                <option key={d.id} value={d.id}>
                  {d.name}
                </option>
              ))}
            </select>
          </label>
        )}
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
            {s.audience ? ` · ${t("voices.occ.previewForDept", { name: s.audience })}` : ""}
          </figcaption>
          <p className="m-0">{s.text}</p>
        </figure>
      ))}
    </div>
  );
}

/* The passages as compact cards (#464): the role, the first lines, the whole
   text on a click. Three first — enough to see the movement — the rest
   behind one toggle, because seven full passages were half the page. */
const FIRST_EXEMPLARS = 3;

function ExemplarGrid({ list }: { list: VoiceExemplar[] }) {
  const { t } = useTranslation();
  const [all, setAll] = useState(false);
  const [open, setOpen] = useState<Set<number>>(new Set());
  const shown = all ? list : list.slice(0, FIRST_EXEMPLARS);
  const toggle = (i: number) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  return (
    <>
      <ul className="vf-ex-grid">
        {shown.map((ex, i) => (
          <li key={i}>
            <button type="button" className={`vf-ex ${open.has(i) ? "open" : ""}`} aria-expanded={open.has(i)} onClick={() => toggle(i)}>
              <span className="vf-ex-h">
                <span className="pill mut">{t(`voices.role.${ex.role}`, ex.role)}</span>
                {ex.from && <span className="muted mono vf-ex-from">{ex.from}</span>}
              </span>
              <span className="vf-ex-text">{ex.text}</span>
            </button>
          </li>
        ))}
      </ul>
      {list.length > FIRST_EXEMPLARS && (
        <button type="button" className="btn sm vf-ex-all" aria-expanded={all} onClick={() => setAll(!all)}>
          {all ? t("voices.flow.showFewer") : t("voices.flow.showAll", { n: list.length })}
        </button>
      )}
    </>
  );
}

/* The release as a review (#464): the card as it reads, beside a sample of
   it; the passages under both; refining and releasing at the end, where a
   person arrives once they have read it. */
function ReleaseStep({ v, editable, onHeard }: { v: VoiceDetail; editable: boolean; onHeard: () => void }) {
  const { t } = useTranslation();
  const inval = useInvalidate(v.id);
  const model = useModel();
  const [card, setCard] = useState<string | null>(null);
  const [editing, setEditing] = useState(false);
  const [instruction, setInstruction] = useState("");
  const [before, setBefore] = useState<string | null>(null);
  const draft = card ?? (v.card || v.released_card);
  const release = useMutation({
    mutationFn: () => post(`/voices/${v.id}/release`, { card: card ?? draft }),
    onSuccess: () => {
      setCard(null);
      setBefore(null);
      setEditing(false);
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
  const drafts = v.draft_exemplars?.length > 0;
  const passages = drafts ? v.draft_exemplars : (v.exemplars ?? []);

  if (!draft) return <p className="muted m-0">{t("voices.cardMissing")}</p>;
  return (
    <div className="vf-review">
      <p className="muted vf-hint">{described ? t("voices.flow.releaseDescribed") : t("voices.cardHint")}</p>
      <div className="vf-review-top">
        <div className="vf-review-card">
          <div className="vf-block-h">
            <span className="vf-block-t">{pending ? t("voices.flow.cardDraft") : t("voices.flow.cardReleased")}</span>
            {editable && (
              <button type="button" className="btn sm ml-auto" aria-pressed={editing} onClick={() => setEditing(!editing)}>
                {editing ? t("voices.flow.showCard") : t("voices.flow.editCard")}
              </button>
            )}
          </div>
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
          {editing ? (
            <textarea
              className="vf-card-edit"
              rows={14}
              value={draft}
              aria-label={pending ? t("voices.flow.cardDraft") : t("voices.flow.cardReleased")}
              onChange={(e) => setCard(e.target.value)}
            />
          ) : (
            before === null && (
              <div className="vf-card-md">
                <Markdown text={draft} baseLevel={4} />
              </div>
            )
          )}
        </div>
        <aside className="vf-review-preview" aria-label={t("voices.flow.step.preview")}>
          <div className="vf-block-h">
            <span className="vf-block-t">{t("voices.flow.step.preview")}</span>
          </div>
          <PreviewStep v={v} editable={editable} onHeard={onHeard} />
        </aside>
      </div>
      {passages.length > 0 && (
        <div className="vf-review-passages">
          <div className="vf-block-h">
            <span className="vf-block-t">
              {drafts ? t("voices.flow.draftExemplars", { n: passages.length }) : t("voices.exemplars")}
            </span>
          </div>
          <p className="muted vf-hint">
            {drafts || described ? t("voices.flow.exemplarsDescribed") : t("voices.exemplarsHint")}
          </p>
          <ExemplarGrid list={passages} />
        </div>
      )}
      {editable && (
        <div className="vf-review-foot">
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
          {!model && <p className="muted text-xs m-0">{t("voices.flow.noModel")}</p>}
          <ErrorLine error={refine.error} />
          <div className="vf-release">
            <span className="muted text-xs">
              {pending
                ? t("voices.flow.releaseEffect")
                : v.released_at
                  ? t("voices.releasedAt", { at: v.released_at.slice(0, 10) })
                  : ""}
            </span>
            <button className="btn primary" disabled={!pending || release.isPending} onClick={() => release.mutate()}>
              {t("voices.release")}
            </button>
          </div>
          <ErrorLine error={release.error} />
        </div>
      )}
    </div>
  );
}

/* --- The two shapes ---------------------------------------------------- */

const STEP_HINT: Partial<Record<StepKey, string>> = {
  purpose: "voices.flow.purposeLead",
  tone: "chatTone.hintVoice",
  preview: "voices.flow.previewLead",
};

/** An existing voice: the six steps as its navigation, one shown at a time.
 *  Which one is the caller's — the summary head's next action moves it too. */
export function VoiceSteps({
  v,
  editable,
  at,
  onPick,
}: {
  v: VoiceDetail;
  editable: boolean;
  at: StepKey;
  onPick: (k: StepKey) => void;
}) {
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
  const i = STEP_KEYS.indexOf(at);
  const step = steps[i];
  const described = v.source === "described";
  const blocker = blockerFor(steps, at);

  let body: ReactNode;
  switch (at) {
    case "purpose":
      body = (
        <>
          <PurposeFields
            purpose={(v.purpose || "") as Purpose}
            onPurpose={(p) => setPurpose.mutate(p)}
            disabled={!editable || setPurpose.isPending}
          />
          <ErrorLine error={setPurpose.error} />
        </>
      );
      break;
    case "source":
      body = (
        <>
          <p className="m-0">
            {described ? t("voices.flow.isDescribed") : t("voices.flow.isMeasured", { documents: v.documents, words: v.words })}
          </p>
          {v.drafted_by && (
            <p className="muted m-0">
              {t("voices.flow.draftedBy")} <Link to={`/agents/${v.drafted_by.id}`}>{v.drafted_by.display_name}</Link>
            </p>
          )}
        </>
      );
      break;
    case "material":
      body = described ? (
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
      );
      break;
    case "tone":
      body = <ToneStep v={v} editable={editable} />;
      break;
    case "preview":
      body =
        step.status === "blocked" ? (
          <p className="muted m-0">{t("voices.flow.block.nothingToHear")}</p>
        ) : (
          <PreviewStep v={v} editable={editable} onHeard={() => setPreviewed(true)} />
        );
      break;
    default:
      body = <ReleaseStep v={v} editable={editable} onHeard={() => setPreviewed(true)} />;
  }

  const hint = STEP_HINT[at];
  return (
    <div className="vf">
      <FlowFrame
        steps={steps}
        at={at}
        onPick={onPick}
        canPick={() => true}
        hint={hint ? t(hint) : undefined}
        nav={
          <>
            <button className="btn sm" disabled={i === 0} onClick={() => onPick(STEP_KEYS[i - 1])}>
              {t("voices.flow.back")}
            </button>
            {/* The reason belongs to the step on screen: once it is done,
                Next only moves on, and a later step's blocker said here
                reads as if this one were still missing something. */}
            <span className="vf-nav-why">
              {blocker && at !== "release" && step.status !== "done" ? t(`voices.flow.block.${blocker}`) : ""}
            </span>
            {i < STEP_KEYS.length - 1 && (
              <button className="btn sm" onClick={() => onPick(STEP_KEYS[i + 1])}>
                {t("voices.flow.next")}
              </button>
            )}
          </>
        }
      >
        {body}
      </FlowFrame>
    </div>
  );
}

/** A new voice: the same six steps, one at a time, and Next only once the
 *  step lets the flow through. The step is in the address (?step=), and once
 *  the voice exists so is the voice (?new=<id>), so a reload comes back to it. */
export function VoiceStepper({ onClose }: { onClose: (created?: string) => void }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const fromURL = params.get("new");
  const id = fromURL && fromURL !== "1" ? fromURL : null;
  const [name, setName] = useState("");
  const [language, setLanguage] = useState("de");
  const [purpose, setPurpose] = useState<Purpose>("");
  const [source, setSource] = useState<SourceChoice>(() => {
    const s = params.get("src");
    return s === "texts" || s === "described" || s === "chat" ? s : "";
  });
  const [previewed, setPreviewed] = useState(false);
  const detail = useQuery({
    queryKey: ["voice", id],
    queryFn: () => api<VoiceDetail>(`/voices/${id}`),
    enabled: !!id,
  });
  const v = id ? detail.data : undefined;
  const go = (step: StepKey, extra: Record<string, string> = {}) =>
    setParams((prev) => {
      const next = new URLSearchParams(prev);
      next.set("step", step);
      for (const [k, x] of Object.entries(extra)) next.set(k, x);
      return next;
    });
  const create = useMutation({
    mutationFn: () => post<Voice>("/voices", { name, language, purpose }),
    onSuccess: (created) => {
      qc.invalidateQueries({ queryKey: ["voices"] });
      go("material", { new: created.id, src: source });
    },
  });
  // Once the voice exists, what it was created with is the voice's.
  const steps = flowSteps({
    name: v ? v.name : name,
    purpose: v ? ((v.purpose || "") as Purpose) : purpose,
    source,
    voice: v,
    previewed,
  });
  const loading = !!id && !v;
  const at = loading ? "material" : stepperStep(steps, params.get("step"), !!id);
  const i = STEP_KEYS.indexOf(at);
  const blocker = blockerFor(steps, at);
  const creates = at === "source" && blocker === "create";
  const last = at === "release";
  const canNext = !loading && !last && (creates ? !create.isPending : blocker === null);
  const next = () => (creates ? create.mutate() : go(STEP_KEYS[i + 1]));
  const canPick = (k: StepKey) => STEP_KEYS.indexOf(k) <= i || reachable(steps, k, !!id);
  const pick = (k: StepKey) => {
    if (id && (k === "purpose" || k === "source")) return;
    if (canPick(k)) go(k);
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
            <input className="mono vf-lang" value={language} disabled={!!id} onChange={(e) => setLanguage(e.target.value)} />
          </label>
        </div>
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
    body = <ToneStep v={v} editable />;
  } else if (at === "preview") {
    body =
      steps[4].status === "blocked" ? (
        <p className="muted">{t("voices.flow.block.nothingToHear")}</p>
      ) : (
        <PreviewStep v={v} editable onHeard={() => setPreviewed(true)} />
      );
  } else {
    body = <ReleaseStep v={v} editable onHeard={() => setPreviewed(true)} />;
  }

  const hint = STEP_HINT[at];
  return (
    <div className="card mb-4 vf vf-new">
      <div className="vf-new-h">
        <h2 className="text-sm">{v ? v.name : t("voices.flow.newTitle")}</h2>
        <button className="btn sm ml-auto" onClick={() => onClose(id ?? undefined)}>
          {id ? t("voices.flow.done") : t("voices.flow.cancel")}
        </button>
      </div>
      <FlowFrame
        steps={steps}
        at={at}
        onPick={pick}
        canPick={(k) => !(id && (k === "purpose" || k === "source")) && canPick(k)}
        hint={hint ? t(hint) : undefined}
        nav={
          <>
            <button className="btn sm" disabled={i === 0 || (!!id && i <= 2)} onClick={() => go(STEP_KEYS[i - 1])}>
              {t("voices.flow.back")}
            </button>
            <span className="vf-nav-why" aria-live="polite">
              {!last && blocker && !creates ? t(`voices.flow.block.${blocker}`) : ""}
            </span>
            {!last && (
              <button className="btn sm primary" disabled={!canNext} onClick={next}>
                {creates ? t("voices.flow.createAndContinue") : t("voices.flow.next")}
              </button>
            )}
            {last && (
              <button className="btn sm" onClick={() => onClose(id ?? undefined)}>
                {t("voices.flow.done")}
              </button>
            )}
          </>
        }
      >
        {body}
      </FlowFrame>
    </div>
  );
}
