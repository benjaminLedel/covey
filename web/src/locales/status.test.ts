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

/* Ein Status wird als Badge gerendert — neben dem Namen eines Agenten, in einer
   Karte und in der Kopfzeile. Was dort steht, ist ein Wort, keine Erklärung:
   „securing the workplace" hat auf der Agentenkarte den Namen „Brunhilde
   Tatkräftig" in eine Spalte von einem Zeichen Breite gepresst.
   Das Layout ist inzwischen dagegen gewappnet (der Name wird gekürzt statt
   zerlegt), aber die Ursache war die Beschriftung, und die gehört ebenfalls
   festgehalten — sonst wandert beim nächsten neuen Status wieder ein halber
   Satz in den Badge. */

// badgeLimit: „sichert Arbeitsplatz" (20) passt, „securing the workplace" (22)
// nicht. Die Grenze liegt bewusst DAZWISCHEN — 22 hätte genau den Satz
// durchgelassen, der das Problem war, und ein Test, der den bekannten Fall
// nicht fängt, ist Möbelstück.
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

  // Mehrsprachig heißt: in jeder Datei, sonst fällt i18n auf den rohen
  // Statusnamen zurück und der Nutzer liest `securing` statt einer Übersetzung.
  it("gibt es in jeder Sprache vollständig", () => {
    const [, enLabels] = statusSets[1];
    for (const [sprache, labels] of statusSets) {
      expect(Object.keys(labels).sort(), sprache).toEqual(Object.keys(enLabels).sort());
    }
  });

  // Die Zustände, die der Server kennt (internal/agents/agents.go), müssen
  // benannt sein — ein Status ohne Beschriftung ist genau der Fall, der als
  // rohes Wort in der Oberfläche landet.
  it("deckt jeden Agentenzustand ab", () => {
    for (const status of ["sleeping", "triggered", "triage", "working", "securing", "killed"]) {
      for (const [sprache, labels] of statusSets) {
        expect(labels[status], `${status} fehlt in ${sprache}`).toBeTruthy();
      }
    }
  });
});
