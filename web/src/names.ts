// Namensgenerator für Agenten: lustige, aber menschlich klingende Botnamen.
// Bodenständiger Vorname + fleißig-freundlicher oder herrlich bürokratischer
// Nachname — klingt wie die Kollegin aus der Poststelle, nicht wie ein
// Server-Hostname. Mehrere Namensmuster (Doppelname, Titel, Mittelinitial,
// Adels-Gag) sorgen für Varianz, damit sich Namen selten wiederholen.

const firstNames = [
  "Bernd", "Uschi", "Detlef", "Heike", "Klaus-Dieter", "Ingrid",
  "Waltraud", "Horst", "Renate", "Günther", "Sieglinde", "Hubert",
  "Edeltraud", "Manfred", "Roswitha", "Egon", "Hannelore", "Norbert",
  "Gisela", "Jürgen", "Lieselotte", "Rüdiger", "Erika", "Karl-Heinz",
  "Frieda", "Achim", "Brunhilde", "Ottfried", "Hildegard", "Volker",
  "Gerlinde", "Reinhold", "Traudl", "Dieter", "Helga", "Wolfgang",
  "Marlies", "Bruno", "Elfriede", "Hartmut", "Käthe", "Lothar",
  "Ursel", "Friedhelm", "Gudrun", "Werner", "Irmgard", "Siegfried",
  "Annegret", "Eberhard", "Meinhard", "Waldtraut", "Kunibert", "Adelheid",
  "Gottfried", "Hertha", "Bodo", "Liesel", "Willibald", "Ottilie",
  "Reinhard", "Gertrud", "Ewald", "Cordula", "Alfons", "Doris",
  "Gunter", "Rosemarie", "Hans-Peter", "Bärbel", "Fiete", "Elke",
  "Knut", "Roswita", "Heinz-Rüdiger", "Mechthild", "Olaf", "Ute",
];

// Tugend-Nachnamen — der bienenfleißige, wohlmeinende Ton.
const virtueSurnames = [
  "Fleißig", "Emsig", "Hurtig", "Wacker", "Redlich", "Munter",
  "Flink", "Tüchtig", "Unverzagt", "Pünktlich", "Gründlich", "Beflissen",
  "Sonnig", "Freundlich", "Rastlos", "Schaffig", "Eifrig", "Tatkräftig",
  "Zuverlässig", "Bienenfleißig", "Sorgfältig", "Rührig", "Betriebsam",
  "Umtriebig", "Hilfsbereit", "Gewissenhaft", "Aufgeweckt", "Strebsam",
  "Findig", "Geflissentlich", "Behände", "Wohlgemut", "Unermüdlich",
  "Dienstbeflissen", "Vorbildlich", "Beherzt", "Wieselflink",
];

// Büro-Nachnamen — die herrlich bürokratische, alberne Note.
const officeSurnames = [
  "Klemmbrett", "Büroklammer", "Tackernagel", "Aktendeckel", "Stempelkissen",
  "Umlaufmappe", "Ablagekorb", "Frankiermeister", "Lochermann", "Heftstreifen",
  "Registerreiter", "Trennblatt", "Posteingang", "Terminkalender", "Klebezettel",
  "Textmarker", "Radiergummi", "Bleistiftspitzer", "Aktenordner", "Sichthülle",
  "Karteikasten", "Pendelmappe", "Kaffeeküche", "Umlaufbeschluss", "Sammelmappe",
  "Notizblock", "Faxgerät", "Wählscheibe", "Vorzimmer", "Konferenzkeks",
  "Sitzungsprotokoll", "Dienstsiegel", "Poststelle", "Hängeregister", "Bürostuhl",
  "Kabelbinder", "Gummiband", "Eddingstift", "Locherstärke", "Rollcontainer",
  "Tischkalender", "Prospekthülle", "Aktenschrank", "Büroklammerich", "Ringbuch",
  "Serienbrief", "Wartemarke", "Kopiervorlage", "Stehordner", "Dienstweg",
];

const titles = ["Dr.", "Prof.", "Dipl.-Ing.", "Mag.", "Dr. Dr."];
const initials = "ABCDEFGHIJKLMNOPRSTUVW".split("");

const randInt = (n: number) => Math.floor(Math.random() * n);
const pick = <T>(list: T[]): T => list[randInt(list.length)];

// Nachname: Büro-Gegenstände (der Witz) etwas häufiger als Tugend-Namen.
function surname(): string {
  return Math.random() < 0.55 ? pick(officeSurnames) : pick(virtueSurnames);
}

// Zwei verschiedene Nachnamen für Doppelnamen.
function twoSurnames(): [string, string] {
  const a = surname();
  let b = surname();
  let guard = 0;
  while (b === a && guard++ < 5) b = surname();
  return [a, b];
}

/**
 * Erzeugt „Renate Büroklammer" + passenden Slug „renate-bueroklammer".
 * Gewichtete Muster für Varianz:
 *  60% Vorname Nachname · 14% Doppelname · 12% Titel · 8% Mittelinitial ·
 *   6% Adels-Gag („von Aktendeckel").
 */
export function generateAgentName(): { name: string; slug: string } {
  const first = pick(firstNames);
  const r = Math.random();
  let name: string;

  if (r < 0.6) {
    name = `${first} ${surname()}`;
  } else if (r < 0.74) {
    const [a, b] = twoSurnames();
    name = `${first} ${a}-${b}`;
  } else if (r < 0.86) {
    name = `${pick(titles)} ${first} ${surname()}`;
  } else if (r < 0.94) {
    name = `${first} ${pick(initials)}. ${surname()}`;
  } else {
    // Adels-Gag: „von" + Büro-Gegenstand klingt am absurdesten.
    name = `${first} von ${pick(officeSurnames)}`;
  }

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
