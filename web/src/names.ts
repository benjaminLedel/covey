// Namensgenerator für Agenten: lustige, aber menschlich klingende Botnamen.
// Kombination aus bodenständigem Vornamen und fleißig-freundlichem Nachnamen —
// klingt wie die Kollegin aus der Poststelle, nicht wie ein Server-Hostname.

const firstNames = [
  "Bernd", "Uschi", "Detlef", "Heike", "Klaus-Dieter", "Ingrid",
  "Waltraud", "Horst", "Renate", "Günther", "Sieglinde", "Hubert",
  "Edeltraud", "Manfred", "Roswitha", "Egon", "Hannelore", "Norbert",
  "Gisela", "Jürgen", "Lieselotte", "Rüdiger", "Erika", "Karl-Heinz",
  "Frieda", "Achim", "Brunhilde", "Ottfried", "Hildegard", "Volker",
];

const lastNames = [
  "Fleißig", "Emsig", "Hurtig", "Wacker", "Redlich", "Munter",
  "Flink", "Tüchtig", "Unverzagt", "Pünktlich", "Gründlich", "Beflissen",
  "Sonnig", "Freundlich", "Rastlos", "Schaffig", "Eifrig", "Tatkräftig",
  "Klemmbrett", "Büroklammer", "Tackernagel", "Aktendeckel", "Stempelkissen",
  "Umlaufmappe", "Ablagekorb", "Frankiermeister",
];

const pick = (list: string[]) => list[Math.floor(Math.random() * list.length)];

/** Erzeugt „Renate Büroklammer" + passenden Slug „renate-bueroklammer". */
export function generateAgentName(): { name: string; slug: string } {
  const name = `${pick(firstNames)} ${pick(lastNames)}`;
  return { name, slug: slugify(name) };
}

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
