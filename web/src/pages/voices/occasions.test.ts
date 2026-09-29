import { describe, it, expect } from "vitest";
import type { Voice } from "../../api";
import { chatVoice, deptSlotValues, parseReason, slotOptions, slotsBody, voiceAssignable } from "./occasions";

// The few rules the voice-per-occasion screens apply themselves (#471): which
// voices a slot offers, what a PUT carries, and how the server's short reason
// reads.

const voice = (over: Partial<Voice> = {}): Voice => ({
  id: "v1",
  name: "Hausstimme",
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
  purpose: "chat",
  description: "",
  draft_exemplars: [],
  ...over,
});

describe("voiceAssignable", () => {
  it("takes a measured voice once it is built with a profile", () => {
    expect(voiceAssignable(voice())).toBe(false);
    expect(voiceAssignable(voice({ version: 1 }))).toBe(false);
    expect(voiceAssignable(voice({ version: 1, profile: { bands: { sentence_len: [8, 14] } } }))).toBe(true);
  });

  it("takes a described voice only once a person released card and passages", () => {
    const described = { source: "described" as const };
    expect(voiceAssignable(voice({ ...described, card: "draft" }))).toBe(false);
    expect(voiceAssignable(voice({ ...described, released_card: "card" }))).toBe(false);
    expect(voiceAssignable(voice({ ...described, released_card: "card", exemplars: [{ role: "opening", text: "Hallo" }] }))).toBe(true);
  });
});

describe("slotOptions", () => {
  it("offers the assignable voices and keeps the one the slot names now", () => {
    const ready = voice({ id: "ready", version: 1, profile: { bands: { x: [0, 1] } } });
    const unbuilt = voice({ id: "unbuilt" });
    const current = voice({ id: "current" });
    expect(slotOptions([ready, unbuilt, current], "current").map((v) => v.id)).toEqual(["ready", "current"]);
    expect(slotOptions([ready, unbuilt, current], "").map((v) => v.id)).toEqual(["ready"]);
  });
});

describe("slotsBody and deptSlotValues", () => {
  it("names every occasion, an empty one as the empty string", () => {
    expect(slotsBody({ chat: "a" })).toEqual({ chat: "a", customers: "", publications: "" });
    expect(deptSlotValues({ publications: "p" })).toEqual({ chat: "", customers: "", publications: "p" });
    expect(deptSlotValues(undefined)).toEqual({ chat: "", customers: "", publications: "" });
  });
});

describe("parseReason", () => {
  it("reads the server's short forms", () => {
    expect(parseReason("org×chat")).toEqual({ level: "org", occasion: "chat" });
    expect(parseReason("agent×customers")).toEqual({ level: "agent", occasion: "customers" });
    expect(parseReason("none×publications")).toEqual({ level: "none", occasion: "publications" });
    expect(parseReason("department:Vertrieb×chat")).toEqual({ level: "department", department: "Vertrieb", occasion: "chat" });
  });

  it("takes the occasion after the last ×, so a department name may carry one", () => {
    expect(parseReason("department:A×B×chat")).toEqual({ level: "department", department: "A×B", occasion: "chat" });
  });

  it("says nothing about what it does not know", () => {
    expect(parseReason("")).toBeNull();
    expect(parseReason("somebody×chat")).toBeNull();
  });
});

describe("chatVoice", () => {
  it("is nothing for a message without a voice", () => {
    expect(chatVoice(undefined)).toBeNull();
    expect(chatVoice({ voice_reason: "none×chat" })).toBeNull();
  });

  it("names the audience the lines came from, else the department whose voice was chosen", () => {
    expect(chatVoice({ voice: "Klar", voice_reason: "department:Vertrieb×chat", audience: "Vertrieb, Einkauf" })).toEqual({
      voice: "Klar",
      audience: "Vertrieb, Einkauf",
      level: "department",
      department: "Vertrieb",
    });
    expect(chatVoice({ voice: "Klar", voice_reason: "department:Vertrieb×chat" })?.audience).toBe("Vertrieb");
    expect(chatVoice({ voice: "Klar", voice_reason: "org×chat" })).toEqual({ voice: "Klar", audience: "", level: "org", department: undefined });
  });
});
