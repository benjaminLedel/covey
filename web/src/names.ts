// Namensgenerator für Agenten: lustige, aber menschlich klingende Botnamen.
// Sprachabhängig (de/en). Zwei Welten, gemischt:
//  - bodenständig: Vorname + fleißiger/bürokratischer Nachname
//    („Renate Büroklammer" / „Reg of Clipboard")
//  - Fantasie: aus Silben erfundene Quatsch-Namen für Varianz
//    („Wuselbert Wibbelzahn" / „Bumblewick Snickerpip")
// Mehrere Namensmuster (Doppelname, Titel, Mittelinitial, Adels-Gag) erhöhen die
// Varianz zusätzlich.

import i18n from "./i18n";

type NamePool = {
  firstNames: string[];
  virtueSurnames: string[]; // Tugend-Nachnamen — der bienenfleißige Ton
  officeSurnames: string[]; // Büro-Nachnamen — die alberne Note
  titles: string[];
  noble: string; // Adels-Partikel: "von" / "of"
  // Fantasie-Silben: Vor-/Nachsilbe für erfundene Vor- und Nachnamen.
  fFirstPre: string[];
  fFirstSuf: string[];
  fSurPre: string[];
  fSurSuf: string[];
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
  fFirstPre: [
    "Wusel", "Knuff", "Schnuffel", "Plörr", "Grummel", "Flausch", "Knuddel",
    "Wibbel", "Zwuckel", "Möppel", "Brummel", "Schlonz", "Muckel", "Fussel",
    "Tröt", "Quassel", "Schnatter", "Kringel", "Pömpel", "Nöhl", "Wubbel",
    "Purzel", "Bibber", "Schrubbel",
  ],
  fFirstSuf: [
    "bert", "hilde", "fried", "traud", "mund", "gunde", "olf", "rich",
    "inchen", "bald", "hard", "linde", "fix", "wupp", "dolf", "gard",
  ],
  fSurPre: [
    "Wibbel", "Schnatter", "Tapp", "Schlonz", "Kringel", "Pömpel", "Nöhl",
    "Rumpel", "Klöter", "Schrubb", "Bibber", "Fizzel", "Knatter", "Wupp",
    "Möpp", "Schlick", "Trödel", "Purzel", "Zwirbel", "Bommel", "Nuckel",
    "Quietsch",
  ],
  fSurSuf: [
    "zahn", "schnut", "bommel", "wupp", "strunz", "knopf", "docht", "pömpel",
    "zwick", "fuß", "näschen", "brömmel", "datsch", "pütz", "kötter", "schwupp",
  ],
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
  fFirstPre: [
    "Bumble", "Snuggle", "Wobble", "Fizz", "Snicker", "Bramble", "Pomple",
    "Wiggle", "Grumble", "Fuddle", "Noodle", "Doodle", "Boffle", "Muddle",
    "Toddle", "Higgle", "Wuzzle", "Crumple", "Snoodle", "Pockle", "Jangle",
    "Wimple",
  ],
  fFirstSuf: [
    "wick", "ory", "bert", "ora", "pip", "kins", "dorf", "boodle",
    "snout", "wump", "dora", "fizzle", "bottom", "willow", "monk", "ington",
  ],
  fSurPre: [
    "Snicker", "Fluffer", "Wiggle", "Bumble", "Pickle", "Noodle", "Wobble",
    "Snuffle", "Cringle", "Higgle", "Squabble", "Doodle", "Puddle", "Grumble",
    "Twiddle", "Fiddle", "Muddle", "Bimble", "Wonker", "Plimp", "Gobble",
    "Whiffle",
  ],
  fSurSuf: [
    "pip", "nutter", "snort", "bottom", "doodle", "wump", "sniff", "puff",
    "knocker", "bee", "whistle", "mop", "pop", "dinkle", "wobble", "britches",
  ],
};

function poolFor(lang: string): NamePool {
  return lang.toLowerCase().startsWith("en") ? en : de;
}

const randInt = (n: number) => Math.floor(Math.random() * n);
const pick = <T>(list: T[]): T => list[randInt(list.length)];
const initials = "ABCDEFGHIJKLMNOPRSTUVW".split("");

// Bodenständiger Nachname: Büro-Gegenstände (der Witz) etwas häufiger.
function surname(p: NamePool): string {
  return Math.random() < 0.55 ? pick(p.officeSurnames) : pick(p.virtueSurnames);
}

function twoSurnames(p: NamePool, sur: () => string): [string, string] {
  const a = sur();
  let b = sur();
  let guard = 0;
  while (b === a && guard++ < 5) b = sur();
  return [a, b];
}

// Erfundene Fantasie-Silben zu Vor- bzw. Nachnamen zusammensetzen. An der Fuge
// entstehende Dreifach-Buchstaben („Knuff"+"fix") auf Doppel kürzen.
const collapse = (s: string) => s.replace(/(.)\1{2,}/g, "$1$1");
const fantasyFirst = (p: NamePool) => collapse(pick(p.fFirstPre) + pick(p.fFirstSuf));
const fantasySurname = (p: NamePool) => collapse(pick(p.fSurPre) + pick(p.fSurSuf));

// baseName wendet die gemeinsamen Muster (Doppelname/Titel/Initial/Adels-Gag)
// auf einen gegebenen Vornamen und einen Nachnamen-Generator an.
function baseName(p: NamePool, first: string, sur: () => string): string {
  const r = Math.random();
  if (r < 0.6) return `${first} ${sur()}`;
  if (r < 0.74) {
    const [a, b] = twoSurnames(p, sur);
    return `${first} ${a}-${b}`;
  }
  if (r < 0.86) return `${pick(p.titles)} ${first} ${sur()}`;
  if (r < 0.94) return `${first} ${pick(initials)}. ${sur()}`;
  return `${first} ${p.noble} ${sur()}`;
}

/**
 * Erzeugt einen Agentennamen passend zur Sprache (Default: aktuelle UI-Sprache).
 * ~40% Fantasie-Namen („Wuselbert Wibbelzahn" / „Bumblewick Snickerpip"),
 * sonst bodenständig („Renate Büroklammer" / „Reg of Clipboard"). Beide nutzen
 * dieselben Muster (Doppelname, Titel, Mittelinitial, Adels-Gag).
 */
export function generateAgentName(lang: string = i18n.language ?? "de"): { name: string; slug: string } {
  const p = poolFor(lang);
  const fantasy = Math.random() < 0.4;
  const first = fantasy ? fantasyFirst(p) : pick(p.firstNames);
  const sur = fantasy ? () => fantasySurname(p) : () => surname(p);
  const name = baseName(p, first, sur);
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
