/* The blocks of a note (#386): the app's block model (mobile/lib/rich/
 * blocks.dart), ported line for line, so that a note edited in the web
 * comes back unchanged in the app and the other way round.
 *
 * One line is one block — that keeps plain text, a transcript, exactly as it
 * was through a parse and a write. Inline markup (**bold**, _italic_,
 * [links](…)) stays in a block's text as Markdown. */

export type BlockKind =
  | "paragraph"
  | "heading1"
  | "heading2"
  | "heading3"
  | "bullet"
  | "numbered"
  | "todo"
  | "quote"
  | "callout"
  | "toggle"
  | "divider"
  | "table"
  | "image";

export type Block = {
  /** Stable for the editing session only: React's key and the drag's id. */
  id: string;
  kind: BlockKind;
  text: string;
  checked?: boolean;
  /** A table's cells, row by row; the first row is the header. */
  rows?: string[][];
  /** A picture's reference: covey-media://<id>, or any URL. */
  ref?: string;
  /** A callout's icon. */
  emoji?: string;
  /** A toggle's content, below its summary (text). */
  body?: string;
  /** Whether a toggle shows its content — this session only. */
  open?: boolean;
};

let seq = 0;
export const newId = () => `b${++seq}`;
export const block = (kind: BlockKind, fields: Partial<Block> = {}): Block => ({ id: newId(), kind, text: "", ...fields });

export const isText = (b: Block) => b.kind !== "divider" && b.kind !== "table" && b.kind !== "image";
/** A list item continues as its own kind on Enter; everything else as a paragraph. */
export const isListItem = (b: Block) => b.kind === "bullet" || b.kind === "numbered" || b.kind === "todo";

const heading = /^(#{1,3})\s+(.*)$/;
const todo = /^\s*[-*]\s+\[( |x|X)\]\s?(.*)$/;
const bullet = /^\s*[-*]\s+(.*)$/;
const numbered = /^\s*\d+\.\s+(.*)$/;
const quote = /^>\s?(.*)$/;
const callout = /^>\s?((?:\p{Extended_Pictographic}|\p{Regional_Indicator}{2})️?)\s+(.*)$/u;
const detailsOpen = /^\s*<details(\s+open)?\s*>\s*(?:<summary>(.*?)<\/summary>)?\s*$/;
const summaryLine = /^\s*<summary>(.*?)<\/summary>\s*$/;
const image = /^!\[[^\]]*\]\(([^)\s]+)\)$/;
const divider = /^(-{3,}|\*{3,}|_{3,})$/;
const tableSep = /^\s*\|?\s*:?-{1,}:?\s*(\|\s*:?-{1,}:?\s*)*\|?\s*$/;

/** A table row's cells. A pipe written as \| belongs to the cell. */
function cells(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const out: string[] = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    if (s[i] === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
    } else if (s[i] === "|") {
      out.push(cur.trim());
      cur = "";
    } else {
      cur += s[i];
    }
  }
  out.push(cur.trim());
  return out;
}

/** Markdown → blocks. */
export function parseBlocks(markdown: string): Block[] {
  const lines = markdown.replace(/\r\n/g, "\n").split("\n");
  const out: Block[] = [];
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    if (line.includes("|") && i + 1 < lines.length && tableSep.test(lines[i + 1]) && lines[i + 1].includes("-")) {
      const rows = [cells(line)];
      i += 2;
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") {
        rows.push(cells(lines[i]));
        i++;
      }
      i--;
      const width = Math.max(...rows.map((r) => r.length));
      for (const r of rows) while (r.length < width) r.push("");
      out.push(block("table", { rows }));
      // The blank line Markdown wants after a table belongs to the table.
      if (i + 1 < lines.length && lines[i + 1].trim() === "") i++;
      continue;
    }
    let m = detailsOpen.exec(line);
    if (m) {
      const end = lines.findIndex((l, j) => j > i && l.trim() === "</details>");
      if (end > 0) {
        let summary: string | undefined = m[2];
        let from = i + 1;
        if (summary === undefined && from < end) {
          const sm = summaryLine.exec(lines[from]);
          if (sm) {
            summary = sm[1];
            from++;
          }
        }
        const content = lines.slice(from, end);
        while (content.length && content[0].trim() === "") content.shift();
        while (content.length && content[content.length - 1].trim() === "") content.pop();
        out.push(block("toggle", { text: summary ?? "", body: content.join("\n") }));
        i = end;
        continue;
      }
    }
    if ((m = image.exec(line.trim()))) out.push(block("image", { ref: m[1] }));
    else if (divider.test(line.trim())) out.push(block("divider"));
    else if ((m = heading.exec(line)))
      out.push(block(m[1].length === 1 ? "heading1" : m[1].length === 2 ? "heading2" : "heading3", { text: m[2] }));
    else if ((m = todo.exec(line))) out.push(block("todo", { text: m[2], checked: m[1] !== " " }));
    else if ((m = bullet.exec(line))) out.push(block("bullet", { text: m[1] }));
    else if ((m = numbered.exec(line))) out.push(block("numbered", { text: m[1] }));
    else if ((m = callout.exec(line))) out.push(block("callout", { emoji: m[1], text: m[2] }));
    else if ((m = quote.exec(line))) out.push(block("quote", { text: m[1] }));
    else out.push(block("paragraph", { text: line }));
  }
  if (out.length === 0) out.push(block("paragraph"));
  return out;
}

const escapeCell = (s: string) => s.replace(/\|/g, "\\|").replace(/\n/g, " ");

/** Blocks → Markdown. Numbered items are renumbered from their run's start. */
export function writeBlocks(blocks: Block[]): string {
  const out: string[] = [];
  let number = 0;
  blocks.forEach((b, i) => {
    number = b.kind === "numbered" ? number + 1 : 0;
    switch (b.kind) {
      case "paragraph":
        out.push(b.text);
        break;
      case "heading1":
        out.push(`# ${b.text}`);
        break;
      case "heading2":
        out.push(`## ${b.text}`);
        break;
      case "heading3":
        out.push(`### ${b.text}`);
        break;
      case "bullet":
        out.push(`- ${b.text}`);
        break;
      case "numbered":
        out.push(`${number}. ${b.text}`);
        break;
      case "todo":
        out.push(`- [${b.checked ? "x" : " "}] ${b.text}`);
        break;
      case "quote":
        out.push(`> ${b.text}`);
        break;
      case "callout":
        out.push(`> ${b.emoji ?? "💡"} ${b.text}`);
        break;
      case "toggle":
        out.push("<details>", `<summary>${b.text}</summary>`, "");
        if (b.body) out.push(b.body, "");
        out.push("</details>");
        break;
      case "divider":
        out.push("---");
        break;
      case "image":
        out.push(`![](${b.ref ?? ""})`);
        break;
      case "table": {
        const rows = b.rows ?? [];
        if (!rows.length) break;
        out.push(`| ${rows[0].map(escapeCell).join(" | ")} |`);
        out.push(`|${Array(rows[0].length).fill(" --- ").join("|")}|`);
        for (const r of rows.slice(1)) out.push(`| ${r.map(escapeCell).join(" | ")} |`);
        if (i < blocks.length - 1) out.push("");
        break;
      }
    }
  });
  return out.join("\n");
}

/** The Markdown source of one text block, as the editor shows it while typing. */
export function sourceOf(b: Block): string {
  return writeBlocks([{ ...b, kind: b.kind === "numbered" ? "numbered" : b.kind }]);
}

/** Turns a block into another kind, keeping its text. */
export function turnInto(b: Block, kind: BlockKind): Block {
  if (kind === "table") return { ...b, kind, text: "", rows: [["", ""], ["", ""]] };
  if (kind === "divider") return { ...b, kind, text: "" };
  if (kind === "callout") return { ...b, kind, emoji: b.emoji ?? "💡" };
  if (kind === "toggle") return { ...b, kind, body: b.body ?? "", open: true };
  return { ...b, kind };
}
