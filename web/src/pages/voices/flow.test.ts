import { describe, it, expect } from "vitest";
import type { VoiceDetail } from "../../api";
import { blockerFor, currentStep, flowSteps, pendingDraft } from "./flow";

// The stepper's one promise: "Next" never leads into a dead end, and whatever
// stands in the way is named. These pin the rule for the three sources.

const voice = (over: Partial<VoiceDetail> = {}): VoiceDetail => ({
  id: "v1",
  name: "Kundendienst",
  language: "de",
  version: 0,
  exemplars: [],
  contrast: [],
  notes: [],
  card: "",
  released_card: "",
  words: 0,
  documents: 0,
  source: "texts",
  purpose: "support_mail",
  description: "",
  draft_exemplars: [],
  corpus: [],
  tone: "",
  checks: [],
  assignable: false,
  ...over,
});

const status = (steps: ReturnType<typeof flowSteps>) => Object.fromEntries(steps.map((s) => [s.key, s.status]));

describe("the voice flow", () => {
  it("asks for a name and a purpose before anything else", () => {
    const steps = flowSteps({ name: "", purpose: "", source: "" });
    expect(blockerFor(steps, "purpose")).toBe("name");
    expect(status(steps).source).toBe("blocked");
    expect(currentStep(steps)).toBe("purpose");
    const named = flowSteps({ name: "Blog", purpose: "", source: "" });
    expect(blockerFor(named, "purpose")).toBe("purpose");
  });

  it("creates the voice when leaving the source step", () => {
    const steps = flowSteps({ name: "Blog", purpose: "blog", source: "texts" });
    expect(blockerFor(steps, "source")).toBe("create");
    expect(status(steps).material).toBe("blocked");
  });

  it("from texts: blocks on missing texts, then on the build", () => {
    let steps = flowSteps({ name: "B", purpose: "blog", source: "texts", voice: voice() });
    expect(blockerFor(steps, "material")).toBe("noTexts");
    expect(currentStep(steps)).toBe("material");
    steps = flowSteps({
      name: "B",
      purpose: "blog",
      source: "texts",
      voice: voice({ corpus: [{ id: "d", name: "a.md", kind: "author", words: 400, created_at: "" }] }),
    });
    expect(blockerFor(steps, "material")).toBe("notBuilt");
    // A reference text alone is not a corpus.
    steps = flowSteps({
      name: "B",
      purpose: "blog",
      source: "texts",
      voice: voice({ corpus: [{ id: "d", name: "ai.md", kind: "reference", words: 400, created_at: "" }] }),
    });
    expect(blockerFor(steps, "material")).toBe("noTexts");
    // Nothing to hear and nothing to release before the material is there.
    expect(status(steps).preview).toBe("blocked");
    expect(blockerFor(steps, "release")).toBe("materialFirst");
  });

  it("a build without a model has no card to release, and says so", () => {
    const steps = flowSteps({ name: "B", purpose: "blog", source: "texts", voice: voice({ version: 1 }) });
    expect(status(steps).material).toBe("done");
    expect(blockerFor(steps, "release")).toBe("noCard");
  });

  it("from a description: the written draft opens tone, preview and release", () => {
    const described = voice({ source: "described", card: "Die Hand.", draft_exemplars: [{ role: "opening", text: "x" }] });
    const steps = flowSteps({ name: "S", purpose: "support_mail", source: "described", voice: described });
    expect(status(steps)).toMatchObject({ material: "done", tone: "optional", preview: "optional", release: "open" });
    // The optional steps never hold the flow up.
    expect(blockerFor(steps, "tone")).toBeNull();
    expect(blockerFor(steps, "preview")).toBeNull();
    expect(blockerFor(steps, "release")).toBe("notReleased");
    expect(pendingDraft(described)).toBe(true);
  });

  it("a released voice with a new refinement is open again", () => {
    const released = voice({ source: "described", card: "Neu.", released_card: "Alt.", released_at: "2026-09-29", exemplars: [{ role: "opening", text: "x" }] });
    expect(status(flowSteps({ name: "S", purpose: "chat", source: "described", voice: released })).release).toBe("open");
    const settled = { ...released, card: "Alt." };
    const steps = flowSteps({ name: "S", purpose: "chat", source: "described", voice: settled, previewed: true });
    expect(status(steps).release).toBe("done");
    expect(status(steps).preview).toBe("done");
  });

  it("from the chat: nothing to do here until the agent has filed the draft", () => {
    const steps = flowSteps({ name: "S", purpose: "blog", source: "chat" });
    expect(status(steps).source).toBe("done");
    expect(blockerFor(steps, "material")).toBe("chat");
  });
});
