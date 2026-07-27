// Namensgenerator für Agenten: lustige, aber menschlich klingende Botnamen.
// Bodenständiger Vorname + fleißig-freundlicher oder herrlich bürokratischer
// Nachname — klingt wie die Kollegin aus der Poststelle, nicht wie ein
// Server-Hostname. Sprachabhängig (de/en) und mit mehreren Namensmustern
// (Doppelname, Titel, Mittelinitial, Adels-Gag) für echte Varianz.

import i18n from "./i18n";

type NamePool = {
  firstNames: string[];
  virtueSurnames: string[]; // Tugend-Nachnamen — der bienenfleißige Ton
  officeSurnames: string[]; // Büro-Nachnamen — die alberne Note
  titles: string[];
  noble: string; // Adels-Partikel: "von" / "of"
};

const de: NamePool = {
  firstNames: [
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
  ],
  virtueSurnames: [
    "Fleißig", "Emsig", "Hurtig", "Wacker", "Redlich", "Munter",
    "Flink", "Tüchtig", "Unverzagt", "Pünktlich", "Gründlich", "Beflissen",
    "Sonnig", "Freundlich", "Rastlos", "Schaffig", "Eifrig", "Tatkräftig",
    "Zuverlässig", "Bienenfleißig", "Sorgfältig", "Rührig", "Betriebsam",
    "Umtriebig", "Hilfsbereit", "Gewissenhaft", "Aufgeweckt", "Strebsam",
    "Findig", "Geflissentlich", "Behände", "Wohlgemut", "Unermüdlich",
    "Dienstbeflissen", "Vorbildlich", "Beherzt", "Wieselflink",
  ],
  officeSurnames: [
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
  ],
  titles: ["Dr.", "Prof.", "Dipl.-Ing.", "Mag.", "Dr. Dr."],
  noble: "von",
};

const en: NamePool = {
  firstNames: [
    "Barbara", "Nigel", "Doris", "Keith", "Brenda", "Colin",
    "Gladys", "Trevor", "Mildred", "Derek", "Norma", "Reg",
    "Ethel", "Clive", "Beryl", "Gordon", "Deirdre", "Malcolm",
    "Sheila", "Roy", "Maureen", "Cyril", "Edna", "Bernard",
    "Pam", "Stan", "Glenda", "Harold", "Vera", "Alan",
    "Sandra", "Ronald", "Pauline", "Terry", "Marjorie", "Dennis",
    "Susan", "Neville", "Carol", "Kenneth", "Enid", "Graham",
    "Sylvia", "Leonard", "Hilda", "Wendy", "Bert", "Agnes",
    "Frank", "Doreen", "Herbert", "Joyce", "Percy", "Iris", "Mavis",
  ],
  virtueSurnames: [
    "Diligent", "Chipper", "Nimble", "Tireless", "Prompt", "Thorough",
    "Steadfast", "Trusty", "Earnest", "Zealous", "Sprightly", "Dutiful",
    "Chirpy", "Sunny", "Keen", "Sturdy", "Plucky", "Brisk",
    "Cheerful", "Hearty", "Willing", "Spry", "Bustling", "Industrious",
    "Eager", "Reliable", "Meticulous", "Nifty", "Perky", "Dapper",
    "Wholesome", "Bright-Eyed", "Can-Do", "Unflagging",
  ],
  officeSurnames: [
    "Paperclip", "Stapler", "Clipboard", "Binder", "Highlighter", "Hole-Punch",
    "Lanyard", "Spreadsheet", "Filofax", "Foolscap", "Letterhead", "Envelope",
    "Postbag", "Rubber-Stamp", "Treasury-Tag", "Bulldog-Clip", "Sticky-Note",
    "Whiteboard", "Pigeonhole", "Inbox", "Memo", "Franking-Machine", "Guillotine",
    "Ledger", "Ring-Binder", "Photocopier", "Watercooler", "Teabag", "Biscuit-Tin",
    "Swivel-Chair", "Fax-Machine", "Rolodex", "Index-Card", "Drawing-Pin",
    "Manila-Folder", "Desk-Tidy", "Correction-Fluid", "Ballpoint", "Minutes",
    "Agenda", "Cubicle", "Kettle", "Lever-Arch", "Flip-Chart", "Name-Badge",
    "Jiffy-Bag", "Comb-Binding", "Hot-Desk", "Tea-Trolley", "Suggestion-Box",
    // Neutralere / US-Büro-Begriffe für weniger UK-Schlagseite:
    "Post-It", "Sharpie", "Legal-Pad", "Scotch-Tape", "Push-Pin", "Thumbtack",
    "Cork-Board", "Dry-Erase", "Mouse-Pad", "File-Cabinet", "Paper-Shredder",
    "Toner-Cartridge", "Notepad", "Cubicle-Wall", "Break-Room", "Standing-Desk",
    "Coffee-Mug", "Desk-Calendar", "Time-Card", "Vending-Machine", "Wite-Out",
    "Dictaphone", "Intercom", "Expense-Report", "Org-Chart", "Water-Cooler",
    "Cubicle-Farm", "Trapper-Keeper", "Paper-Tray", "Rubber-Band", "Copy-Machine",
  ],
  titles: ["Dr.", "Prof.", "Sir", "Dame", "Rev."],
  noble: "of",
};

function poolFor(lang: string): NamePool {
  return lang.toLowerCase().startsWith("en") ? en : de;
}

const randInt = (n: number) => Math.floor(Math.random() * n);
const pick = <T>(list: T[]): T => list[randInt(list.length)];
const initials = "ABCDEFGHIJKLMNOPRSTUVW".split("");

// Nachname: Büro-Gegenstände (der Witz) etwas häufiger als Tugend-Namen.
function surname(p: NamePool): string {
  return Math.random() < 0.55 ? pick(p.officeSurnames) : pick(p.virtueSurnames);
}

// Zwei verschiedene Nachnamen für Doppelnamen.
function twoSurnames(p: NamePool): [string, string] {
  const a = surname(p);
  let b = surname(p);
  let guard = 0;
  while (b === a && guard++ < 5) b = surname(p);
  return [a, b];
}

/**
 * Erzeugt einen Agentennamen passend zur Sprache (Default: aktuelle UI-Sprache):
 * de → „Renate Büroklammer", en → „Reg of Clipboard". Gewichtete Muster:
 *  60% Vorname Nachname · 14% Doppelname · 12% Titel · 8% Mittelinitial ·
 *   6% Adels-Gag („von"/„of").
 */
export function generateAgentName(lang: string = i18n.language ?? "de"): { name: string; slug: string } {
  const p = poolFor(lang);
  const first = pick(p.firstNames);
  const r = Math.random();
  let name: string;

  if (r < 0.6) {
    name = `${first} ${surname(p)}`;
  } else if (r < 0.74) {
    const [a, b] = twoSurnames(p);
    name = `${first} ${a}-${b}`;
  } else if (r < 0.86) {
    name = `${pick(p.titles)} ${first} ${surname(p)}`;
  } else if (r < 0.94) {
    name = `${first} ${pick(initials)}. ${surname(p)}`;
  } else {
    // Adels-Gag: Partikel + Büro-Gegenstand klingt am absurdesten.
    name = `${first} ${p.noble} ${pick(p.officeSurnames)}`;
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
