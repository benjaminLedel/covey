// Namen für Agenten.
//
// Der Generator selbst wohnt seit spec/20 im Binary (internal/agents/names.go):
// die Setup-Strecke und die Personalabteilung brauchen ihn serverseitig, und
// zwei Pools, die auseinanderdriften, wären schlechter als ein Fetch. Hier
// bleibt der Würfel-Aufruf — und `slugify`, weil das Feld beim Tippen live
// mitläuft und dafür kein Netzwerk befragt werden darf.

import { api } from "./api";
import i18n from "./i18n";

export type RolledName = { name: string; slug: string };

/**
 * Würfelt einen Agentennamen in der aktuellen UI-Sprache.
 * ~40% Fantasie-Namen („Wuselbert Wibbelzahn" / „Bumblewick Snickerpip"),
 * sonst bodenständig („Renate Büroklammer" / „Reg of Clipboard").
 */
export function rollAgentName(lang: string = i18n.language ?? "de"): Promise<RolledName> {
  return api<RolledName>(`/names/roll?lang=${encodeURIComponent(lang)}`);
}

// Muss mit agents.Slugify im Binary übereinstimmen — der Server würfelt den
// Slug zum gewürfelten Namen, diese Fassung füllt ihn beim Tippen.
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
