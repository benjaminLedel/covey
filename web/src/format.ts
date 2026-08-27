// Kleine Formatierer, die sich mehrere Ansichten teilen. Bewusst ohne i18n:
// Die Einheiten (s/min/h/d) sind in beiden Sprachen dieselben, den Satz drumherum
// bauen die Aufrufer über ihre eigenen Übersetzungsschlüssel.

// fmtBytes bringt eine Dateigröße auf die gröbste Einheit, die sie noch
// beschreibt — „812 B", „14,2 kB", „3,1 MB". Genutzt vom Dateibrowser des
// Arbeitsplatzes.
export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["kB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}

// fmtUSD bringt einen Betrag auf so viele Nachkommastellen, wie er noch trägt —
// „2.746 $", „12,30 $", „0,0042 $". Ein einzelner Lauf kostet oft Bruchteile
// eines Cents; auf zwei Stellen gerundet stünde dort überall 0,00 $. Genutzt
// von der Kostenseite und vom Backlog (Kosten an der Aufgabenkarte).
//
// Ab tausend mit Tausendertrennung, denn genau dort fängt sie an zu helfen:
// „2746 $" liest man ziffernweise, „2.746 $" auf einen Blick.
export function fmtUSD(v: number): string {
  if (v >= 1000) return `${group(Math.round(v))} $`;
  if (v >= 1) return `${v.toFixed(2)} $`;
  return `${v.toFixed(4)} $`;
}

// fmtCount bringt eine Anzahl auf das, was ein Mensch beim Überfliegen
// aufnimmt — „842", „12,3 k", „148 M".
//
// Der Anlass war eine Zahl aus der Kostenansicht: 147952885 gelesene
// Cache-Tokens. Das liest niemand als hundertachtundvierzig Millionen, das
// zählt man ziffernweise nach. Gedacht für Tokens, Blöcke, Dateien — alles,
// wovon eine Instanz sechsstellig viele hat.
//
// Die genaue Zahl geht dabei nicht verloren: wer sie braucht (eine Rechnung
// prüfen), findet sie im `title` daneben — dafür ist exact() da.
export function fmtCount(n: number): string {
  const v = Math.round(n);
  if (v < 10_000) return group(v);
  if (v < 1_000_000) return `${trim(v / 1000)} k`;
  return `${trim(v / 1_000_000)} M`;
}

// exact ist die Langfassung für den Tooltip neben einer gekürzten Zahl.
export function exact(n: number): string {
  return group(Math.round(n));
}

// group setzt Tausenderpunkte. Ohne Intl-Locale, wie der Rest dieser Datei:
// die Einheiten sind in beiden Sprachen dieselben, und eine Zahl, die je nach
// Browsersprache anders aussieht, macht Screenshots und Fehlerberichte
// unvergleichbar.
function group(v: number): string {
  const neg = v < 0;
  const digits = Math.abs(v).toString();
  let out = "";
  for (let i = 0; i < digits.length; i++) {
    if (i > 0 && (digits.length - i) % 3 === 0) out += ".";
    out += digits[i];
  }
  return neg ? `-${out}` : out;
}

// trim zeigt eine Nachkommastelle, solange sie etwas sagt: „12,3 k", aber
// „148 M" und nicht „148,0 M".
function trim(v: number): string {
  const one = v.toFixed(1);
  const rounded = one.endsWith(".0") ? one.slice(0, -2) : one;
  return rounded.replace(".", ",");
}

// fmtDelta bringt eine Zeitspanne in Millisekunden auf die gröbste Einheit, die
// sie noch beschreibt — „42 s", „3 min", „2 h 15 min", „4 d". Genutzt vom
// Heartbeat (nächster/letzter Lauf) und vom Activity-Feed (Dauer eines Sub-Laufs).
export function fmtDelta(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h} h ${m % 60} min` : `${h} h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d} d ${h % 24} h` : `${d} d`;
}
