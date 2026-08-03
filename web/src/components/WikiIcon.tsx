import type { ReactNode } from "react";

// Symbole für das Wiki-Gedächtnis (spec/05): Seitentypen und Protokoll-Vorgänge.
//
// Inline-SVG wie bei den Zielsystem-Symbolen (TargetIcon) — die SPA wird per
// //go:embed ins Binary gezogen und darf keine externen Assets nachladen, also
// kommt kein Icon-Paket in Frage. Gleiche Strichstärke und Rundungen, damit die
// Symbole neben denen der Zielsysteme nicht aus dem Rahmen fallen.
//
// Alle sind rein dekorativ (aria-hidden): Typ und Vorgang stehen im Markup
// immer als Wort daneben. Ein Symbol allein trüge die Bedeutung nicht.

const typeGlyphs: Record<string, ReactNode> = {
  // Aktenkoffer — die Geschäftsbeziehung.
  kunde: (
    <>
      <rect x="3" y="7" width="18" height="13" rx="2" />
      <path d="M8 7V5.5A1.5 1.5 0 0 1 9.5 4h5A1.5 1.5 0 0 1 16 5.5V7" />
    </>
  ),
  // Ordner.
  projekt: <path d="M3 7a2 2 0 0 1 2-2h3.6l2 2H19a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z" />,
  // Server-Einschübe.
  system: (
    <>
      <rect x="3" y="4.5" width="18" height="6.5" rx="1.5" />
      <rect x="3" y="13" width="18" height="6.5" rx="1.5" />
      <path d="M6.8 7.75h.01M6.8 16.25h.01" />
    </>
  ),
  // Kopf und Schultern.
  person: (
    <>
      <circle cx="12" cy="8" r="3.5" />
      <path d="M5.5 20a6.5 6.5 0 0 1 13 0" />
    </>
  ),
  // Warndreieck.
  problem: (
    <>
      <path d="M12 4.5 3 19.5h18z" />
      <path d="M12 10v4M12 16.8h.01" />
    </>
  ),
  // Anhänger.
  thema: (
    <>
      <path d="M3.5 12.6V5a1.5 1.5 0 0 1 1.5-1.5h7.6l7.9 7.9a1.5 1.5 0 0 1 0 2.1l-6.9 6.9a1.5 1.5 0 0 1-2.1 0z" />
      <circle cx="8" cy="8" r="1.2" />
    </>
  ),
};

// Ohne Typ: gestrichelter Kreis — nicht zugeordnet, kein Fehler.
const untypedGlyph = <circle cx="12" cy="12" r="7.5" strokeDasharray="3 3" />;

const opGlyphs: Record<string, ReactNode> = {
  // Neu hinzugekommen.
  ingest: (
    <>
      <circle cx="12" cy="12" r="7.75" />
      <path d="M12 8.5v7M8.5 12h7" />
    </>
  ),
  // Bearbeitet.
  write: <path d="M4.5 19.5h3.6L19 8.6a1.9 1.9 0 0 0-2.7-2.7L5.2 17z" />,
  // Zwei Stränge werden einer.
  merge: (
    <>
      <circle cx="6" cy="5.5" r="2" />
      <circle cx="6" cy="18.5" r="2" />
      <circle cx="18" cy="12" r="2" />
      <path d="M8 6.4 15.9 11M8 17.6 15.9 13" />
    </>
  ),
  // Papierkorb.
  delete: <path d="M4.5 7h15M9.5 7V4.8h5V7M6.5 7l.9 12.2h9.2L17.5 7" />,
};

const common = (size: number) => ({
  width: size,
  height: size,
  viewBox: "0 0 24 24",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.7,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  "aria-hidden": true as const,
  focusable: "false" as const,
});

/** Symbol eines Seitentyps. Leerer Typ ergibt den gestrichelten Kreis. */
export function WikiTypeIcon({ type, size = 15, className }: { type?: string; size?: number; className?: string }) {
  const glyph = (type && typeGlyphs[type]) || untypedGlyph;
  return (
    <svg {...common(size)} className={className}>
      {glyph}
    </svg>
  );
}

/** Symbol eines Protokoll-Vorgangs. `append` teilt sich das Symbol mit `write`. */
export function WikiOpIcon({ op, size = 14, className }: { op: string; size?: number; className?: string }) {
  const glyph = opGlyphs[op === "append" ? "write" : op];
  if (!glyph) return null;
  return (
    <svg {...common(size)} className={className}>
      {glyph}
    </svg>
  );
}
