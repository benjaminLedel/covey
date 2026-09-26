import { Fragment, type ReactNode } from "react";

/* A block's text as it reads when it is not being edited (#386): **bold**,
 * *italic* / _italic_, `code`, ~~strike~~ and [links](https://…), the inline
 * Markdown the app's editor styles (mobile/lib/rich/inline.dart). Everything
 * else is text — no HTML is ever interpreted, so a note cannot carry a
 * script into the page. Only http(s) and mailto links become links. */
// Underscores emphasise only at word edges: 21_09_2026.pdf stays a name.
const token = /(\*\*[^*\n]+\*\*|(?<!\w)__[^_\n]+__(?!\w)|~~[^~\n]+~~|`[^`\n]+`|\*[^*\s][^*\n]*\*|(?<!\w)_[^_\n]+_(?!\w)|\[[^\]\n]+\]\([^)\s]+\))/g;

export function Inline({ text }: { text: string }): ReactNode {
  if (!text) return null;
  const parts = text.split(token);
  return parts.map((p, i) => {
    if (i % 2 === 0) return <Fragment key={i}>{p}</Fragment>;
    if (p.startsWith("**") || p.startsWith("__")) return <strong key={i}>{p.slice(2, -2)}</strong>;
    if (p.startsWith("~~")) return <s key={i}>{p.slice(2, -2)}</s>;
    if (p.startsWith("`")) return <code key={i}>{p.slice(1, -1)}</code>;
    if (p.startsWith("[")) {
      const m = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(p);
      if (m && /^(https?:|mailto:)/i.test(m[2])) {
        return (
          <a key={i} href={m[2]} target="_blank" rel="noreferrer noopener" onClick={(e) => e.stopPropagation()}>
            {m[1]}
          </a>
        );
      }
      return <Fragment key={i}>{p}</Fragment>;
    }
    return <em key={i}>{p.slice(1, -1)}</em>;
  });
}
