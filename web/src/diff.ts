// Line-wise diff for the suggestions of covey Doctor (spec/21).
//
// Deliberately no dependency: what is needed here is the comparison of two
// Markdown files of a few dozen lines, and pulling a library into the bundle
// for that would cost more than the twenty lines below.

export type DiffLine = { kind: "same" | "add" | "del"; text: string };

// Above this size lines are no longer compared one by one: the LCS table is
// O(n·m), and a config file with a thousand lines is not one that someone
// reads as a diff anyway. Then the old block stands against the new one.
const MAX_LINES = 600;

/** diffLines compares two texts line by line (longest common subsequence). */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = before === "" ? [] : before.split("\n");
  const b = after === "" ? [] : after.split("\n");
  if (a.length > MAX_LINES || b.length > MAX_LINES) {
    return [
      ...a.map((text): DiffLine => ({ kind: "del", text })),
      ...b.map((text): DiffLine => ({ kind: "add", text })),
    ];
  }

  // lcs[i][j] = length of the longest common subsequence of a[i…] and b[j…].
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
 * collapse shortens long unchanged stretches to `context` lines at each
 * edge of a change. What falls away in between is reported as one line with
 * `skipped` — otherwise three changed lines in a 200-line PLAYBOOKS.md can no
 * longer be read.
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
