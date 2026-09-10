import { Fragment, type ReactNode } from "react";

// Kleiner, abhängigkeitsfreier Markdown-Renderer für die Assistent-Bubbles
// (FR-001). Deckt das ab, was das Modell typischerweise liefert: Überschriften,
// Listen, Codeblöcke, Inline-Code, fett/kursiv und Links. Bewusst KEIN
// dangerouslySetInnerHTML — es wird zu React-Elementen geparst, sodass React
// jeden Text automatisch escaped (kein HTML-Injection-Vektor aus der LLM-Antwort).

// renderInline parst Inline-Auszeichnung: `code`, **fett**, *kursiv*/_kursiv_,
// [text](url). Reihenfolge über eine gemeinsame Regex; Text dazwischen bleibt
// unverändert (und wird von React escaped).
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
      // Nur sichere Schemata verlinken; sonst als Text belassen.
      if (/^(https?:|mailto:)/i.test(href)) {
        nodes.push(
          <a key={key} href={href} target="_blank" rel="noopener noreferrer">{label}</a>,
        );
      } else if (/^\/(?!\/)/.test(href)) {
        // Absolut-relative Adresse auf dieser Instanz (/docs/…). Ohne diesen
        // Zweig blieb jeder interne Verweis stummer Text — die Docs konnten
        // sich nicht untereinander verlinken, weder für Leser noch für
        // Suchmaschinen. Kein target="_blank": das eigene Haus öffnet man
        // nicht in einem neuen Fenster. Das (?!\/) hält "//fremde.example"
        // draußen, das protokollrelativ nach außen führt.
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

// Die Trennzeile einer Tabelle: |---|---:|:--:|. Sie ist das einzige sichere
// Erkennungszeichen — ein Rohr allein steht auch mitten in einem Satz.
const TABLE_SEP = /^\s*\|?(\s*:?-+:?\s*\|)+\s*:?-*:?\s*\|?\s*$/;

// cells zerlegt eine Zeile in ihre Zellen. Führendes und schließendes Rohr sind
// in GFM optional, deshalb fliegen sie vor dem Trennen weg — sonst stünde am
// Anfang und Ende je eine leere Spalte.
function cells(row: string): string[] {
  return row.replace(/^\s*\|/, "").replace(/\|\s*$/, "").split("|").map((c) => c.trim());
}

// alignments liest die Ausrichtung je Spalte aus der Trennzeile. Für einen
// Zahlenbericht ist das kein Schmuck: rechtsbündige Zahlen vergleicht das Auge
// stellenweise, linksbündige nicht.
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

// isTableStart: diese Zeile ist die Kopfzeile UND die nächste die Trennzeile.
// Beides zusammen, weil eine Kopfzeile ohne Trennzeile in GFM keine Tabelle
// ist — und ein Absatz, der zufällig ein Rohr enthält, einer bleiben soll.
function isTableStart(lines: string[], i: number): boolean {
  return lines[i].includes("|") && i + 1 < lines.length && TABLE_SEP.test(lines[i + 1]);
}

// baseLevel: welche HTML-Ebene ein `#` bekommt.
//
// Der Vorgabewert 4 gilt für die Stellen, an denen dieser Renderer eine
// Antwort INNERHALB einer Seite darstellt — Assistent-Bubbles, Dateivorschau,
// Wiki-Ausschnitte. Dort wäre ein h1 gelogen: die Seite hat ihre Überschrift
// schon, und ein zweites h1 im Dokument ist für Screenreader wie für
// Suchmaschinen eine falsche Aussage über den Aufbau.
//
// Wo der Markdown-Text DIE Seite ist — der Docs-Bereich der Website —, ist
// genau das Gegenteil richtig, und dort steht baseLevel={1}. Vorher rendete
// auch dort jedes `#` als h4, und keine einzige Docs-Seite hatte eine
// Hauptüberschrift.
export function Markdown({ text, baseLevel = 4 }: { text: string; baseLevel?: number }) {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const blocks: ReactNode[] = [];
  let i = 0;
  let key = 0;

  while (i < lines.length) {
    const line = lines[i];

    // Leere Zeile → Blocktrenner.
    if (line.trim() === "") { i++; continue; }

    // Codeblock ```…```
    if (line.trimStart().startsWith("```")) {
      const buf: string[] = [];
      i++;
      while (i < lines.length && !lines[i].trimStart().startsWith("```")) {
        buf.push(lines[i]);
        i++;
      }
      i++; // schließendes ``` überspringen
      blocks.push(
        <pre key={key++} className="md-pre"><code>{buf.join("\n")}</code></pre>,
      );
      continue;
    }

    // Überschrift # / ## / ###
    const h = /^(#{1,3})\s+(.*)$/.exec(line);
    if (h) {
      const content = renderInline(h[2], `h${key}`);
      // h1..h6 — tiefer geht HTML nicht, und eine vierte Ebene braucht der
      // Renderer nicht zu können.
      const level = Math.min(baseLevel + h[1].length - 1, 6);
      const Tag = `h${level}` as "h1" | "h2" | "h3" | "h4" | "h5" | "h6";
      blocks.push(<Tag key={key++} className="md-h">{content}</Tag>);
      i++; // Zeile konsumieren — sonst Endlosschleife bei Überschriften
      continue;
    }

    // Ungeordnete Liste (-, *) bzw. geordnete Liste (1.)
    const isUl = /^\s*[-*]\s+/.test(line);
    const isOl = /^\s*\d+\.\s+/.test(line);
    if (isUl || isOl) {
      const items: ReactNode[] = [];
      while (i < lines.length && (isUl ? /^\s*[-*]\s+/ : /^\s*\d+\.\s+/).test(lines[i])) {
        const item = lines[i].replace(isUl ? /^\s*[-*]\s+/ : /^\s*\d+\.\s+/, "");
        items.push(<li key={items.length}>{renderInline(item, `li${key}-${items.length}`)}</li>);
        i++;
      }
      blocks.push(
        isUl ? <ul key={key++} className="md-list">{items}</ul>
        : <ol key={key++} className="md-list">{items}</ol>,
      );
      continue;
    }

    // Tabelle: Kopfzeile, Trennzeile aus Strichen, Datenzeilen.
    //
    // Ohne diesen Zweig fiel eine Tabelle in den Absatz-Zweig und stand als
    // eine Reihe von Rohren im Fließtext (#225). Sie ist keine Kür: sobald ein
    // Agent Zahlen berichtet, greift er zur Tabelle — und genau dort wird die
    // Ausgabe unlesbar, wo sie am meisten zu sagen hat.
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
        // Der Rahmen scrollt, nicht die Seite: eine breite Tabelle darf das
        // Layout auf einem schmalen Fenster nicht auseinanderziehen.
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

    // Absatz: aufeinanderfolgende Nicht-Leerzeilen mit <br> verbinden.
    const para: string[] = [];
    while (
      i < lines.length &&
      lines[i].trim() !== "" &&
      !lines[i].trimStart().startsWith("```") &&
      !/^(#{1,3})\s+/.test(lines[i]) &&
      !/^\s*[-*]\s+/.test(lines[i]) &&
      !/^\s*\d+\.\s+/.test(lines[i]) &&
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
