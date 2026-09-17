import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";

export function wikiInline(
  text: string,
  key: string,
  has: (slug: string) => boolean,
  onNav: (slug: string) => void,
  missingLabel: string,
): ReactNode[] {
  const out: ReactNode[] = [];
  const re = /\[\[([^\]]+)\]\]|\*\*([^*]+)\*\*/g;
  let last = 0;
  let m: RegExpExecArray | null;
  let i = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push(text.slice(last, m.index));
    if (m[1] != null) {
      const slug = m[1].trim();
      const ok = has(slug);
      out.push(
        <button
          key={`${key}-${i}`}
          type="button"
          className={`wikilink${ok ? "" : " missing"}`}
          onClick={ok ? () => onNav(slug) : undefined}
          title={ok ? undefined : missingLabel}
        >
          {slug}
        </button>,
      );
    } else if (m[2] != null) {
      out.push(<strong key={`${key}-${i}`}>{m[2]}</strong>);
    }
    last = re.lastIndex;
    i++;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

// Renders a page body as light Markdown (headings #/##/###,
// lists -/*, paragraphs) with clickable [[Wikilinks]] — a real
// wiki page instead of raw text.
export function WikiBody({ text, has, onNav }: { text: string; has: (slug: string) => boolean; onNav: (slug: string) => void }) {
  const { t } = useTranslation();
  const missing = t("agent.memory.missing");
  const blocks: ReactNode[] = [];
  let bullets: ReactNode[] = [];
  const flush = (k: string) => {
    if (bullets.length) {
      blocks.push(<ul key={`ul-${k}`}>{bullets}</ul>);
      bullets = [];
    }
  };
  text.split("\n").forEach((raw, idx) => {
    const line = raw.trimEnd();
    const li = /^\s*[-*]\s+(.*)$/.exec(line);
    if (li) {
      bullets.push(<li key={`li-${idx}`}>{wikiInline(li[1], `li${idx}`, has, onNav, missing)}</li>);
      return;
    }
    flush(String(idx));
    if (!line.trim()) return;
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    if (h) {
      blocks.push(
        <div key={idx} className={`wiki-h wiki-h${h[1].length}`}>
          {wikiInline(h[2], `h${idx}`, has, onNav, missing)}
        </div>,
      );
      return;
    }
    blocks.push(<p key={idx}>{wikiInline(line, `p${idx}`, has, onNav, missing)}</p>);
  });
  flush("end");
  return <div className="wiki-body voice text-[14.5px]">{blocks}</div>;
}

// Short preview for the index list: strip markup, [[slug]] → slug.
export function wikiPreview(text: string): string {
  return text
    .replace(/\[\[([^\]]+)\]\]/g, "$1")
    .replace(/[*#>`]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 120);
}

// Order of page types in the tree (spec/05). Empty type comes last: the
// unclassified pages are a remainder, not a beginning.
export const WIKI_TYPES = ["kunde", "projekt", "system", "person", "problem", "thema", ""] as const;

// Sorting within one tree level. The choice lives in localStorage — anyone
// who works by relevance does not want to set it again on every page visit.
export type WikiSort = "recent" | "relevance" | "title";
export const WIKI_SORT_KEY = "covey.wiki.sort";
export const WIKI_SORTS: WikiSort[] = ["recent", "relevance", "title"];

// linkContext pulls out the sentence in which one page links to another.
// A backlink without that sentence forces a click just to see why.
export function linkContext(body: string, slug: string): string {
  const re = new RegExp("[^.\\n]*\\[\\[" + slug.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\]\\][^.\\n]*");
  const m = re.exec(body);
  if (!m) return "";
  const s = m[0].replace(/\s+/g, " ").trim();
  return s.length > 150 ? s.slice(0, 149) + "…" : s;
}
