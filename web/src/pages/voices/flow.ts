import type { Voice, VoiceDocument } from "../../api";

/** A voice as the flow reads it: the list's shape, with the corpus when the
 *  detail has been fetched. Without the corpus the document count stands in
 *  for "texts are in" — enough for the summary head of a closed voice. */
export type FlowVoice = Voice & { corpus?: VoiceDocument[] };

/* The guided way to a voice (#458): six steps, and at every moment what is
   done, what is next and what stands in the way.

   Kept apart from the page so that the rule is readable — and tested — on its
   own: which step blocks the next is the one thing the stepper must never get
   wrong, because a "Next" that leads into a dead end teaches people to stop
   trusting it. */

export type StepKey = "purpose" | "source" | "material" | "tone" | "preview" | "release";
export const STEP_KEYS: StepKey[] = ["purpose", "source", "material", "tone", "preview", "release"];

/** Where the voice comes from. "chat" is not stored: an agent files the draft. */
export type SourceChoice = "" | "texts" | "described" | "chat";

export type Purpose = "" | "blog" | "support_mail" | "chat" | "offers" | "other";
export const PURPOSES: Exclude<Purpose, "">[] = ["blog", "support_mail", "chat", "offers", "other"];

/** done: nothing left to do. open: can be done now. optional: may be skipped.
 *  blocked: cannot be done yet, and `blocker` says why. */
export type StepStatus = "done" | "open" | "optional" | "blocked";

export type Step = {
  key: StepKey;
  status: StepStatus;
  /** An i18n key under voices.flow.block, set when the step is blocked or
   *  not yet done — the sentence that says what is missing. */
  blocker?: string;
  /** Whether the flow may move past this step while it is not done. */
  required: boolean;
};

export type FlowInput = {
  name: string;
  purpose: Purpose;
  source: SourceChoice;
  /** The voice once it exists. */
  voice?: FlowVoice;
  /** Whether a sample has been heard in this session. */
  previewed?: boolean;
};

/** Whether a draft waits for release: a card that differs from the released
 *  one, or model-written exemplars. */
export function pendingDraft(v: Pick<Voice, "card" | "released_card" | "draft_exemplars">): boolean {
  const card = (v.card ?? "").trim();
  return (card !== "" && card !== (v.released_card ?? "").trim()) || (v.draft_exemplars?.length ?? 0) > 0;
}

/** Whether there is anything to hear or to release. */
export function hasVoiceText(v: FlowVoice): boolean {
  return (
    (v.card ?? "").trim() !== "" ||
    (v.released_card ?? "").trim() !== "" ||
    (v.exemplars?.length ?? 0) > 0 ||
    (v.draft_exemplars?.length ?? 0) > 0
  );
}

export function flowSteps(input: FlowInput): Step[] {
  const { voice } = input;
  const out: Step[] = [];

  const purposeDone = input.name.trim() !== "" && input.purpose !== "";
  out.push({
    key: "purpose",
    required: true,
    status: purposeDone ? "done" : "open",
    blocker: purposeDone ? undefined : input.name.trim() === "" ? "name" : "purpose",
  });

  // A voice is stored as "texts" until a description has been written for
  // it, so the person's choice counts until the voice says otherwise — and a
  // built voice is measured, whatever was chosen.
  const source: SourceChoice =
    voice?.source === "described"
      ? "described"
      : voice && voice.version > 0
        ? "texts"
        : input.source === "described"
          ? "described"
          : voice
            ? "texts"
            : input.source;
  const sourceDone = source !== "" && (source === "chat" || voice !== undefined);
  out.push({
    key: "source",
    required: true,
    // A voice that exists has its source, whatever else is missing.
    status: sourceDone ? "done" : !purposeDone ? "blocked" : "open",
    blocker: sourceDone ? undefined : !purposeDone ? "purposeFirst" : source === "" ? "source" : "create",
  });

  // Material: the texts built, or the description written.
  let material: Step;
  if (source === "chat") {
    material = { key: "material", required: true, status: "blocked", blocker: "chat" };
  } else if (!voice) {
    material = { key: "material", required: true, status: "blocked", blocker: "create" };
  } else if (source === "texts") {
    const built = voice.version > 0;
    const texts = voice.corpus ? voice.corpus.some((d) => d.kind !== "reference") : voice.documents > 0;
    const blocker = built ? undefined : texts ? "notBuilt" : "noTexts";
    material = { key: "material", required: true, status: built ? "done" : "open", blocker };
  } else {
    const written = hasVoiceText(voice);
    material = {
      key: "material",
      required: true,
      status: written ? "done" : "open",
      blocker: written ? undefined : "noDescription",
    };
  }
  out.push(material);
  const materialDone = material.status === "done";

  // The chat tone is set, not built, and may stay empty: the organisation's
  // default then applies.
  const toneSet = voice?.chat_tone && Object.values(voice.chat_tone).some((x) => x);
  out.push({
    key: "tone",
    required: false,
    status: !voice ? "blocked" : toneSet ? "done" : "optional",
    blocker: !voice ? "create" : undefined,
  });

  out.push({
    key: "preview",
    required: false,
    status: !materialDone ? "blocked" : input.previewed ? "done" : "optional",
    blocker: !materialDone ? "nothingToHear" : undefined,
  });

  let release: Step;
  if (!voice || !materialDone) {
    release = { key: "release", required: true, status: "blocked", blocker: "materialFirst" };
  } else if ((voice.card ?? "").trim() === "" && (voice.released_card ?? "").trim() === "") {
    // A measured voice built without a model has no card to release. It can
    // still be carried — the measured parts act — so this blocks the release,
    // not the voice.
    release = { key: "release", required: true, status: "blocked", blocker: "noCard" };
  } else if (voice.released_at && !pendingDraft(voice)) {
    release = { key: "release", required: true, status: "done" };
  } else {
    release = { key: "release", required: true, status: "open", blocker: "notReleased" };
  }
  out.push(release);
  return out;
}

/** The step a person should be on: the first required one not done, or the
 *  first open optional one after it. */
export function currentStep(steps: Step[]): StepKey {
  const first = steps.find((s) => s.status !== "done" && (s.required || s.status === "open"));
  return first?.key ?? "release";
}

/** Why the flow cannot move from `at` to the next step, or null if it can. */
export function blockerFor(steps: Step[], at: StepKey): string | null {
  const s = steps.find((x) => x.key === at);
  if (!s) return null;
  if (s.status === "blocked") return s.blocker ?? null;
  if (s.required && s.status !== "done") return s.blocker ?? null;
  return null;
}

/** Done steps out of all, for the progress line. */
export function progress(steps: Step[]): { done: number; total: number } {
  return { done: steps.filter((s) => s.status === "done").length, total: steps.length };
}

export function isStepKey(k: string | null | undefined): k is StepKey {
  return !!k && (STEP_KEYS as string[]).includes(k);
}

/** Whether a new voice's stepper may show step `k`: every step before it lets
 *  the flow through. Once the voice exists its first two steps are its own —
 *  changed on its page, not by walking back — and the source counts as passed. */
export function reachable(steps: Step[], k: StepKey, created: boolean): boolean {
  if (created && (k === "purpose" || k === "source")) return false;
  const target = STEP_KEYS.indexOf(k);
  return STEP_KEYS.slice(0, target).every((key) => blockerFor(steps, key) === null || (key === "source" && created));
}

/** The step to show for a new voice: the one the address names when the
 *  stepper may be there, else the one the person should be on. A reload
 *  loses what was typed but not yet stored, so the address cannot skip past it. */
export function stepperStep(steps: Step[], param: string | null, created: boolean): StepKey {
  if (isStepKey(param) && reachable(steps, param, created)) return param;
  const at = currentStep(steps);
  return created && (at === "purpose" || at === "source") ? "material" : at;
}

/** The step an existing voice opens on: the one the address names, else the
 *  first that needs attention — which is the release once nothing does. */
export function voiceStep(steps: Step[], param: string | null): StepKey {
  return isStepKey(param) ? param : currentStep(steps);
}

/** The one thing to do next, for the summary head: which step, and what the
 *  button says. `null` when nothing waits for a person. */
export type NextAction = { step: StepKey; label: "purpose" | "addTexts" | "build" | "describe" | "release" };

export function nextAction(steps: Step[]): NextAction | null {
  const s = (k: StepKey) => steps.find((x) => x.key === k)!;
  if (s("purpose").status !== "done") return { step: "purpose", label: "purpose" };
  const material = s("material");
  if (material.status !== "done") {
    if (material.blocker === "noTexts") return { step: "material", label: "addTexts" };
    if (material.blocker === "notBuilt") return { step: "material", label: "build" };
    if (material.blocker === "noDescription") return { step: "material", label: "describe" };
    return null;
  }
  if (s("release").status === "open") return { step: "release", label: "release" };
  return null;
}
