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
// „1234 $", „12,30 $", „0,0042 $". Ein einzelner Lauf kostet oft Bruchteile
// eines Cents; auf zwei Stellen gerundet stünde dort überall 0,00 $. Genutzt
// von der Kostenseite und vom Backlog (Kosten an der Aufgabenkarte).
export function fmtUSD(v: number): string {
  if (v >= 1000) return `${v.toFixed(0)} $`;
  if (v >= 1) return `${v.toFixed(2)} $`;
  return `${v.toFixed(4)} $`;
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
