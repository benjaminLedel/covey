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

// Rendert einen Seiten-Body als leichtes Markdown (Überschriften #/##/###,
// Aufzählungen -/*, Absätze) mit klickbaren [[Wikilinks]] — eine echte
// Wiki-Seite statt Rohtext.
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

// Kurzvorschau für die Index-Liste: Markup entfernen, [[slug]] → slug.
export function wikiPreview(text: string): string {
  return text
    .replace(/\[\[([^\]]+)\]\]/g, "$1")
    .replace(/[*#>`]/g, "")
    .replace(/\s+/g, " ")
    .trim()
    .slice(0, 120);
}

// Reihenfolge der Seitentypen im Baum (spec/05). Leerer Typ kommt zuletzt: die
// nicht eingeordneten Seiten sind ein Rest, kein Anfang.
export const WIKI_TYPES = ["kunde", "projekt", "system", "person", "problem", "thema", ""] as const;

// Sortierung innerhalb einer Baumebene. Die Wahl steht im localStorage — wer
// nach Relevanz arbeitet, will das nicht bei jedem Seitenaufruf neu einstellen.
export type WikiSort = "recent" | "relevance" | "title";
export const WIKI_SORT_KEY = "covey.wiki.sort";
export const WIKI_SORTS: WikiSort[] = ["recent", "relevance", "title"];

// linkContext zieht den Satz heraus, in dem eine Seite auf eine andere verweist.
// Ein Backlink ohne diesen Satz zwingt zum Klicken, nur um zu sehen, warum.
export function linkContext(body: string, slug: string): string {
  const re = new RegExp("[^.\\n]*\\[\\[" + slug.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + "\\]\\][^.\\n]*");
  const m = re.exec(body);
  if (!m) return "";
  const s = m[0].replace(/\s+/g, " ").trim();
  return s.length > 150 ? s.slice(0, 149) + "…" : s;
}
