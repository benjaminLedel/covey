import type { TFunction } from "i18next";
import type { Voice, VoiceUse } from "../../api";

export type { VoiceUse };

/* Voices per occasion (#471). An agent, a department and the organisation
   each name a voice per occasion; the one in effect is chosen by who is
   spoken to — the department of the person addressed, then the agent, then
   the organisation. The server resolves; this file only carries its shapes
   and the few rules the screens need to read them. */

export const OCCASIONS = ["chat", "customers", "publications"] as const;
export type Occasion = (typeof OCCASIONS)[number];

export type Level = "department" | "agent" | "org" | "none";

/** A filled slot; null is an empty one. */
export type SlotRef = { voice_id: string; voice: string } | null;
export type Slots = Record<Occasion, SlotRef>;

/** One voice in effect and why ("org×chat", "department:Sales×chat"). */
export type Cell = { voice_id: string | null; voice: string; level: Level; reason: string };

export type AgentVoices = { slots: Slots; effective: Record<Occasion, Cell> };
export type OrgVoices = { slots: Slots };

export type AssignmentRow = {
  department: { id: string; name: string } | null;
  audience_note: string;
  cells: Record<Occasion, Cell>;
};
export type Assignments = { occasions: Occasion[]; rows: AssignmentRow[] };

/** The server's bound on a department's audience note. */
export const AUDIENCE_NOTE_MAX = 400;

/* Whether an agent may carry the voice yet — the server's Voice.Assignable:
   a described voice once a person released it, a measured one once it is
   built with a profile. The server refuses anything else; the select only
   does not offer it. */
export function voiceAssignable(v: Pick<Voice, "source" | "released_card" | "exemplars" | "version" | "profile">): boolean {
  if (v.source === "described") return !!v.released_card && (v.exemplars?.length ?? 0) > 0;
  return v.version > 0 && Object.keys(v.profile?.bands ?? {}).length > 0;
}

/* The voices a slot may be set to: the assignable ones, plus whatever the
   slot names now — a voice that stopped being assignable must still read as
   what it is, not as an empty select. */
export function slotOptions(voices: Voice[], current: string): Voice[] {
  return voices.filter((v) => voiceAssignable(v) || v.id === current);
}

/** A slot's id for a select: "" when empty. */
export const slotValue = (s: SlotRef | undefined) => s?.voice_id ?? "";

/** The body of PUT /agents/{id}/voices and PUT /departments/{id}/voices:
 *  every occasion, "" for an empty one. */
export function slotsBody(values: Partial<Record<Occasion, string>>): Record<Occasion, string> {
  return Object.fromEntries(OCCASIONS.map((o) => [o, values[o] ?? ""])) as Record<Occasion, string>;
}

/** A department's `voices` map (id per occasion) as select values. */
export function deptSlotValues(voices: Record<string, string> | undefined): Record<Occasion, string> {
  return Object.fromEntries(OCCASIONS.map((o) => [o, voices?.[o] ?? ""])) as Record<Occasion, string>;
}

/* The reason in its parts. The short form is the server's (voice.Choice
   .Reason): "department:<name>×<occasion>", "agent×<occasion>",
   "org×<occasion>", "none×<occasion>". A department name may itself carry
   "×", so the occasion is what follows the LAST one. */
export function parseReason(reason: string): { level: Level; department?: string; occasion: string } | null {
  const i = reason.lastIndexOf("×");
  if (i < 0) return null;
  const head = reason.slice(0, i);
  const occasion = reason.slice(i + "×".length);
  if (head.startsWith("department:")) return { level: "department", department: head.slice("department:".length), occasion };
  if (head === "agent" || head === "org" || head === "none") return { level: head, occasion };
  return null;
}

/* What a chat message says about its voice (meta from the server, #471):
   the voice's name, whom it was written for, and why that voice. Null when
   the message names no voice — an older one, or one from an instance
   without voices. "For" is the departments whose lines went with the turn;
   without any, the department whose voice was chosen. */
export function chatVoice(meta: Record<string, string> | undefined): {
  voice: string;
  audience: string;
  level: Level | null;
  department?: string;
} | null {
  const voice = meta?.voice?.trim();
  if (!voice) return null;
  const why = parseReason(meta?.voice_reason ?? "");
  return {
    voice,
    audience: meta?.audience?.trim() || why?.department || "",
    level: why?.level ?? null,
    department: why?.department,
  };
}

/** Why a voice is in effect, in words: "organisation default", "department Sales". */
export function levelText(t: TFunction, cell: Pick<Cell, "level" | "reason">): string {
  if (cell.level === "department") {
    return t("voices.occ.level.department", { name: parseReason(cell.reason)?.department ?? "" });
  }
  return t(`voices.occ.level.${cell.level}`);
}
