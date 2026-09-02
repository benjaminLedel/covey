// Zeilenweiser Diff für die Vorschläge von covey Doctor (spec/21).
//
// Bewusst keine Abhängigkeit: was hier gebraucht wird, ist der Vergleich
// zweier Markdown-Dateien von ein paar Dutzend Zeilen, und dafür eine Bibliothek
// ins Bundle zu ziehen wäre teurer als die zwanzig Zeilen darunter.

export type DiffLine = { kind: "same" | "add" | "del"; text: string };

// Ab dieser Größe wird nicht mehr Zeile für Zeile verglichen: die LCS-Tabelle
// ist O(n·m), und eine Config-Datei mit tausend Zeilen ist ohnehin keine, die
// jemand als Diff liest. Dann steht der alte Block gegen den neuen.
const MAX_LINES = 600;

/** diffLines vergleicht zwei Texte zeilenweise (längste gemeinsame Teilfolge). */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    return [
      ...a.map((text): DiffLine => ({ kind: "del", text })),
      ...b.map((text): DiffLine => ({ kind: "add", text })),
    ];
  }

  // lcs[i][j] = Länge der längsten gemeinsamen Teilfolge von a[i…] und b[j…].
  const lcs: number[][] = Array.from({ length: a.length + 1 }, () => new Array(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      lcs[i][j] = a[i] === b[j] ? lcs[i + 1][j + 1] + 1 : Math.max(lcs[i + 1][j], lcs[i][j + 1]);
    }
  }

  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i] });
      i++;
      j++;
    } else if (lcs[i + 1][j] >= lcs[i][j + 1]) {
      out.push({ kind: "del", text: a[i++] });
    } else {
      out.push({ kind: "add", text: b[j++] });
    }
  }
  while (i < a.length) out.push({ kind: "del", text: a[i++] });
  while (j < b.length) out.push({ kind: "add", text: b[j++] });
  return out;
}

/**
 * collapse kürzt lange unveränderte Strecken auf `context` Zeilen an jedem
 * Rand einer Änderung ein. Was dazwischen wegfällt, wird als eine Zeile mit
 * `skipped` gemeldet — sonst liest man in einer 200-Zeilen-PLAYBOOKS.md drei
 * geänderte Zeilen nicht mehr.
 */
export type DiffChunk = DiffLine | { kind: "skip"; skipped: number };

export function collapse(lines: DiffLine[], context = 3): DiffChunk[] {
  const keep = new Array(lines.length).fill(false);
  lines.forEach((l, idx) => {
    if (l.kind === "same") return;
    for (let k = Math.max(0, idx - context); k <= Math.min(lines.length - 1, idx + context); k++) {
      keep[k] = true;
    }
  });
  const out: DiffChunk[] = [];
  let skipped = 0;
  lines.forEach((l, idx) => {
    if (keep[idx]) {
      if (skipped > 0) {
        out.push({ kind: "skip", skipped });
        skipped = 0;
      }
      out.push(l);
    } else {
      skipped++;
    }
  });
  if (skipped > 0) out.push({ kind: "skip", skipped });
  return out;
}
