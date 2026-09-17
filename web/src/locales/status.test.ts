import { describe, expect, it } from "vitest";
import de from "./de.json";
import en from "./en.json";
import es from "./es.json";
import fr from "./fr.json";
import italienisch from "./it.json";
import nl from "./nl.json";
import pl from "./pl.json";
import pt from "./pt.json";
import ja from "./ja.json";
import zh from "./zh.json";

/* A status is rendered as a badge — next to the name of an agent, in a
   card and in the header. What stands there is a word, not an explanation:
   `securing the workplace` pressed the name `Brunhilde
   Tatkräftig` into a column one character wide on the agent card.
   The layout is guarded against that by now (the name is truncated instead
   of broken apart), but the cause was the label, and that belongs
   recorded as well — otherwise the next new status moves half a
   sentence into the badge again. */

// badgeLimit: `sichert Arbeitsplatz` (20) fits, `securing the workplace` (22)
// does not. The limit sits deliberately BETWEEN them — 22 would have let
// through exactly the sentence that was the problem, and a test that does not
// catch the known case is furniture.
const badgeLimit = 20;

const statusSets: [string, Record<string, string>][] = [
  ["de", de.status as Record<string, string>],
  ["en", en.status as Record<string, string>],
  ["es", es.status as Record<string, string>],
  ["fr", fr.status as Record<string, string>],
  ["it", italienisch.status as Record<string, string>],
  ["nl", nl.status as Record<string, string>],
  ["pl", pl.status as Record<string, string>],
  ["pt", pt.status as Record<string, string>],
  ["ja", ja.status as Record<string, string>],
  ["zh", zh.status as Record<string, string>],
];

describe("Status-Beschriftungen", () => {
  it.each(statusSets)("bleiben in %s kurz genug für einen Badge", (_sprache, labels) => {
    const zuLang = Object.entries(labels).filter(([, text]) => text.length > badgeLimit);
    expect(zuLang).toEqual([]);
  });

  // Multilingual means: in every file, otherwise i18n falls back to the raw
  // status name and the user reads `securing` instead of a translation.
  it("gibt es in jeder Sprache vollständig", () => {
    const [, enLabels] = statusSets[1];
    for (const [sprache, labels] of statusSets) {
      expect(Object.keys(labels).sort(), sprache).toEqual(Object.keys(enLabels).sort());
    }
  });

  // The states the server knows (internal/agents/agents.go) have to be
  // named — a status without a label is exactly the case that lands as a
  // raw word in the UI.
  it("deckt jeden Agentenzustand ab", () => {
    for (const status of ["sleeping", "triggered", "triage", "working", "securing", "killed"]) {
      for (const [sprache, labels] of statusSets) {
        expect(labels[status], `${status} fehlt in ${sprache}`).toBeTruthy();
      }
    }
  });
});
