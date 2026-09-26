import { Fragment, type ReactNode } from "react";

// Small dependency-free Markdown renderer for the assistant bubbles (FR-001).
// Covers what the model typically delivers: headings, lists, code blocks,
// inline code, bold/italic and links. Deliberately no dangerouslySetInnerHTML
// — it parses into React elements, so React escapes every text by itself (no
// HTML injection vector out of the LLM answer).

// renderInline parses inline markup: `code`, **bold**, *italic*/_italic_,
// [text](url). Order comes from one shared regex; the text in between stays
// as it is (and React escapes it).
const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*]+\*|_[^_]+_)|(\[[^\]]+\]\([^)]+\))/g;

function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  INLINE.lastIndex = 0;
  while ((m = INLINE.exec(text)) !== null) {
    if (m.index > last) nodes.push(text.slice(last, m.index));
    const tok = m[0];
    const key = `${keyPrefix}-${i++}`;
    if (m[1]) {
      nodes.push(<code key={key}>{tok.slice(1, -1)}</code>);
    } else if (m[2]) {
      nodes.push(<strong key={key}>{tok.slice(2, -2)}</strong>);
    } else if (m[3]) {
      nodes.push(<em key={key}>{tok.slice(1, -1)}</em>);
    } else if (m[4]) {
      const link = /^\[([^\]]+)\]\(([^)]+)\)$/.exec(tok);
      const label = link?.[1] ?? tok;
      const href = link?.[2] ?? "";
      // Only link safe schemes; otherwise leave it as text.
      if (/^(https?:|mailto:)/i.test(href)) {
        nodes.push(
          <a key={key} href={href} target="_blank" rel="noopener noreferrer">{label}</a>,
        );
      } else if (/^\/(?!\/)/.test(href)) {
        // Root-relative address on this instance (/docs/…). Without this
        // branch every internal link stayed silent text — the docs could not
        // link to each other, neither for readers nor for search engines. No
        // target="_blank": one does not open one's own house in a new window.
        // The (?!\/) keeps "//fremde.example" out, which leads outside as
        // protocol-relative.
        nodes.push(<a key={key} href={href}>{label}</a>);
      } else {
        nodes.push(label);
      }
    }
    last = m.index + tok.length;
  }
  if (last < text.length) nodes.push(text.slice(last));
  return nodes;
}

// The separator row of a table: |---|---:|:--:|. It is the only reliable
// sign — a pipe on its own also stands in the middle of a sentence.
const TABLE_SEP = /^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-*:?\s*\|?\s*$/;

// cells splits a row into its cells. Leading and trailing pipe are optional
// in GFM, so they go away before the split — otherwise there would stand an
// empty column at the start and at the end.
function cells(row: string): string[] {
  return row.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|").map((c) => c.trim());
}

// alignments reads the alignment per column from the separator row. For a
// number report this is no decoration: right-aligned numbers the eye compares
// piecewise, left-aligned ones not.
function alignments(sep: string): ("left" | "center" | "right" | undefined)[] {
  return cells(sep).map((c) => {
    const left = c.startsWith(":");
    const right = c.endsWith(":");
    if (left && right) return "center";
    if (right) return "right";
    if (left) return "left";
    return undefined;
  });
}

// isTableStart: this row is the header row AND the next one the separator row.
// Both together, because a header row without a separator row is no table in
// GFM — and a paragraph that happens to hold a pipe should stay one.
function isTableStart(lines: string[], i: number): boolean {
  return lines[i].includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]);
}

// baseLevel: which HTML level a `#` gets.
//
// The default of 4 holds for the places where this renderer shows an answer
// INSIDE a page — assistant bubbles, file preview, wiki excerpts. An h1 there
// would lie: the page already has its heading, and a second h1 in the document
// is a false claim about the structure, for screen readers as for search
// engines.
//
// Where the Markdown text IS the page — the docs area of the website — the
// exact opposite is right, and there baseLevel={1} stands. Before, every
// `#` rendered as h4 there as well, and not a single docs page came with a
// main heading.
/* A line that is a picture the page allows: its resolved source, else null. */
function shownImage(line: string, resolveImage?: (src: string) => string | null) {
  if (!resolveImage) return null;
  const m = /^!\[([^\]]*)\]\(([^)\s]+)\)$/.exec(line.trim());
  const src = m ? resolveImage(m[2]) : null;
  return m && src ? { src, alt: m[1] } : null;
}

/* resolveImage decides whether a picture is shown at all (#344): without it,
   `![](…)` stays text — Markdown from a model or an agent must not load
   images from anywhere it likes (a tracking pixel is an image). The notes
   page passes one that turns covey-media://<id> into its own endpoint. */
export function Markdown({
  text,
  baseLevel = 4,
  resolveImage,
}: {
  text: string;
  baseLevel?: number;
  resolveImage?: (src: string) => string | null;
}) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Empty line → block separator.
    if (line.trim() === "") { i++; continue; }

    // Code block ```…```
    if (line.trimStart().startsWith("```")) {
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith("```")) {
        buf.push(lines[i]);
        i++;
      }
      i++; // skip the closing ```
      blocks.push(
        <pre key={key++} className="md-pre"><code>{buf.join("\n")}</code></pre>,
      );
      continue;
    }

    // A picture on its own line, where the page allows it.
    const shown = shownImage(line, resolveImage);
    if (shown) {
      blocks.push(<img key={key++} className="md-img" src={shown.src} alt={shown.alt} loading="lazy" />);
      i++;
      continue;
    }

    // A divider.
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(line.trim())) {
      blocks.push(<hr key={key++} className="md-hr" />);
      i++;
      continue;
    }

    // A toggle (#371): <details>, its <summary>, the content, </details> —
    // what the app's note editor writes. The content is Markdown of its own.
    if (/^\s*<details(\s+open)?\s*>/.test(line)) {
      const end = lines.findIndex((l, n) => n > i && l.trim() === "</details>");
      if (end > 0) {
        let summary = /<summary>(.*?)<\/summary>/.exec(line)?.[1];
        let from = i + 1;
        if (summary === undefined && from < end) {
          const sm = /^\s*<summary>(.*?)<\/summary>\s*$/.exec(lines[from]);
          if (sm) { summary = sm[1]; from++; }
        }
        const inner = lines.slice(from, end).join("\n");
        blocks.push(
          <details key={key++} className="md-toggle">
            <summary>{renderInline(summary ?? "", `t${key}`)}</summary>
            <div className="md-toggle-body">
              <Markdown text={inner} baseLevel={baseLevel} resolveImage={resolveImage} />
            </div>
          </details>,
        );
        i = end + 1;
        continue;
      }
    }

    // A callout (#371): a quote line that begins with an emoji.
    const callout = /^>\s?(\p{Extended_Pictographic}\uFE0F?|\p{Regional_Indicator}{2})\s+(.*)$/u.exec(line);
    if (callout) {
      blocks.push(
        <div key={key++} className="md-callout">
          <span className="md-callout-icon" aria-hidden="true">{callout[1]}</span>
          <div>{renderInline(callout[2], `c${key}`)}</div>
        </div>,
      );
      i++;
      continue;
    }

    // A quote: consecutive "> " lines.
    if (/^>\s?/.test(line)) {
      const buf: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i])) {
        buf.push(lines[i].replace(/^>\s?/, ""));
        i++;
      }
      blocks.push(
        <blockquote key={key++} className="md-quote">
          {buf.map((l, idx) => (
            <Fragment key={idx}>
              {idx > 0 && <br />}
              {renderInline(l, `q${key}-${idx}`)}
            </Fragment>
          ))}
        </blockquote>,
      );
      continue;
    }

    // Heading # / ## / ###
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    if (h) {
      const content = renderInline(h[2], `h${key}`);
      // h1..h6 — HTML goes no deeper, and the renderer need not be able to
      // do a fourth level.
      const level = Math.min(baseLevel + h[1].length - 1, 6);
      const Tag = `h${level}` as "h1" | "h2" | "h3" | "h4" | "h5" | "h6";
      blocks.push(<Tag key={key++} className="md-h">{content}</Tag>);
      i++; // consume the line — otherwise an endless loop on headings
      continue;
    }

    // Unordered list (-, *) or ordered list (1.)
    const isUl = /^\s*[-*]\s+/.test(line);
    const isOl = /^\s*\d+\.\s+/.test(line);
    if (isUl || isOl) {
      const items: ReactNode[] = [];
      while (i < lines.length && (isUl ? /^\s*[-*]\s+/ : /^\s*\d+\.\s+/).test(lines[i])) {
        const item = lines[i].replace(isUl ? /^\s*[-*]\s+/ : /^\s*\d+\.\s+/, "");
        /* A task item ("- [ ] …", "- [x] …"): a meeting summary's action items
           are a checklist (#342). A real checkbox, read-only — the state is
           the model's report, not something to tick here. */
        const task = isUl ? /^\[( |x|X)\]\s+(.*)$/.exec(item) : null;
        items.push(
          task ? (
            <li key={items.length} className="md-task">
              <input type="checkbox" checked={task[1] !== " "} readOnly disabled />
              <span>{renderInline(task[2], `li${key}-${items.length}`)}</span>
            </li>
          ) : (
            <li key={items.length}>{renderInline(item, `li${key}-${items.length}`)}</li>
          ),
        );
        i++;
      }
      blocks.push(
        isUl ? <ul key={key++} className="md-list">{items}</ul>
        : <ol key={key++} className="md-list">{items}</ol>,
      );
      continue;
    }

    // Table: header row, separator row of dashes, data rows.
    //
    // Without this branch a table fell into the paragraph branch and stood as
    // a row of pipes in the running text (#225). It is no extra credit: when
    // an agent reports numbers it reaches for the table — and exactly there
    // the output turns unreadable, where it has the most to say.
    if (isTableStart(lines, i)) {
      const head = cells(lines[i]);
      const align = alignments(lines[i + 1]);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && lines[i].includes("|") && lines[i].trim() !== "") {
        rows.push(cells(lines[i]));
        i++;
      }
      const k = key++;
      blocks.push(
        // The frame scrolls, not the page: a wide table must not pull the
        // layout apart on a narrow window.
        <div key={k} className="md-table-wrap">
          <table className="md-table">
            <thead>
              <tr>
                {head.map((c, n) => (
                  <th key={n} style={align[n] ? { textAlign: align[n] } : undefined}>
                    {renderInline(c, `th${k}-${n}`)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, rn) => (
                <tr key={rn}>
                  {r.map((c, n) => (
                    <td key={n} style={align[n] ? { textAlign: align[n] } : undefined}>
                      {renderInline(c, `td${k}-${rn}-${n}`)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>,
      );
      continue;
    }

    // Paragraph: join consecutive non-empty lines with <br>.
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !lines[i].trimStart().startsWith("```") &&
      !/^(#{1,3})\s+/.test(lines[i]) &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
      !/^>\s?/.test(lines[i]) &&
      !/^(-{3,}|\*{3,}|_{3,})$/.test(lines[i].trim()) &&
      // Only a picture that is shown ends a paragraph; one the page does not
      // allow stays text in it. Excluding every picture line here left a
      // disallowed one in no branch at all, and the loop never advanced.
      !shownImage(lines[i], resolveImage) &&
      !isTableStart(lines, i)
    ) {
      para.push(lines[i]);
      i++;
    }
    blocks.push(
      <p key={key++} className="md-p">
        {para.map((l, idx) => (
          <Fragment key={idx}>
            {idx > 0 && <br />}
            {renderInline(l, `p${key}-${idx}`)}
          </Fragment>
        ))}
      </p>,
    );
  }

  return <>{blocks}</>;
}
