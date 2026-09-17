// Names for agents.
//
// The generator itself has lived in the binary since spec/20
// (internal/agents/names.go): the setup flow and the department need it
// server-side, and two pools drifting apart would be worse than a fetch. What
// stays here is the roll call — and `slugify`, because the field runs live
// while typing and may not query the network for that.

import { api } from "./api";
import i18n from "./i18n";

export type RolledName = { name: string; slug: string };

/**
 * Rolls an agent name in the current UI language.
 * ~40% invented names (`Wuselbert Wibbelzahn` / "Bumblewick Snickerpip"),
 * otherwise down-to-earth (`Renate Büroklammer` / "Reg of Clipboard").
 */
export function rollAgentName(lang: string = i18n.language ?? "de"): Promise<RolledName> {
  return api<RolledName>(`/names/roll?lang=${encodeURIComponent(lang)}`);
}

// Must match agents.Slugify in the binary — the server rolls the slug to the
// rolled name, this version fills it while typing.
export function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/ä/g, "ae")
    .replace(/ö/g, "oe")
    .replace(/ü/g, "ue")
    .replace(/ß/g, "ss")
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}
