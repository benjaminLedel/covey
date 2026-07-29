import type { ReactNode } from "react";

// Kleine Logos/Symbole für Zielsysteme. Bewusst als Inline-SVG: die SPA wird
// per //go:embed ins Binary gezogen und darf keine externen Assets nachladen.
//
// Die Auflösung ist gestuft und fällt immer weich zurück — hier steht KEINE
// Plugin-Liste, die etwas gaten würde, nur eine Darstellungs-Zuordnung:
//   1. Plugin-Name  (Built-ins, "gitlab" → Tanuki)
//   2. Plugin-Art   ("mcp", "custom")
//   3. Kategorie    ("ticketing" → Ticket)
//   4. generisch    (Baustein)
// Ein neues Plugin bekommt so ohne Änderung hier ein passendes Kategorie-
// Symbol; ein eigenes Logo ist die Kür, nicht die Pflicht.

// Marken-Signets: Pfade aus Simple Icons (CC0), unverändert. Gefüllt und in
// der Markenfarbe — sie sollen als Logo lesbar sein.
const brandMarks: Record<string, { path: string; color: string; title: string }> = {
  gitlab: {
    title: "GitLab",
    color: "#FC6D26",
    path: "m23.6004 9.5927-.0337-.0862L20.3.9814a.851.851 0 0 0-.3362-.405.8748.8748 0 0 0-.9997.0539.8748.8748 0 0 0-.29.4399l-2.2055 6.748H7.5375l-2.2057-6.748a.8573.8573 0 0 0-.29-.4412.8748.8748 0 0 0-.9997-.0537.8585.8585 0 0 0-.3362.4049L.4332 9.5015l-.0325.0862a6.0657 6.0657 0 0 0 2.0119 7.0105l.0113.0087.03.0213 4.976 3.7264 2.462 1.8633 1.4995 1.1321a1.0085 1.0085 0 0 0 1.2197 0l1.4995-1.1321 2.4619-1.8633 5.006-3.7489.0125-.01a6.0682 6.0682 0 0 0 2.0094-7.003z",
  },
  nextcloud: {
    title: "Nextcloud",
    color: "#0082C9",
    path: "M12.018 6.537c-2.5 0-4.6 1.712-5.241 4.015-.56-1.232-1.793-2.105-3.225-2.105A3.569 3.569 0 0 0 0 12a3.569 3.569 0 0 0 3.552 3.553c1.432 0 2.664-.874 3.224-2.106.641 2.304 2.742 4.016 5.242 4.016 2.487 0 4.576-1.693 5.231-3.977.569 1.21 1.783 2.067 3.198 2.067A3.568 3.568 0 0 0 24 12a3.569 3.569 0 0 0-3.553-3.553c-1.416 0-2.63.858-3.199 2.067-.654-2.284-2.743-3.978-5.23-3.977zm0 2.085c1.878 0 3.378 1.5 3.378 3.378 0 1.878-1.5 3.378-3.378 3.378A3.362 3.362 0 0 1 8.641 12c0-1.878 1.5-3.378 3.377-3.378zm-8.466 1.91c.822 0 1.467.645 1.467 1.468s-.644 1.467-1.467 1.468A1.452 1.452 0 0 1 2.085 12c0-.823.644-1.467 1.467-1.467zm16.895 0c.823 0 1.468.645 1.468 1.468s-.645 1.468-1.468 1.468A1.452 1.452 0 0 1 18.98 12c0-.823.644-1.467 1.467-1.467z",
  },
};

// Symbole in der Bildsprache der Oberfläche: Strich, 1.7, runde Enden,
// currentColor. Für Systeme, deren Logo markenrechtlich nicht frei ist
// (Microsoft, Zammad), und für alles Generische.
const glyphs: Record<string, ReactNode> = {
  ticket: (
    <>
      <path d="M3 8.6c0-.9.7-1.6 1.6-1.6h14.8c.9 0 1.6.7 1.6 1.6v1.7a1.7 1.7 0 0 0 0 3.4v1.7c0 .9-.7 1.6-1.6 1.6H4.6A1.6 1.6 0 0 1 3 15.4v-1.7a1.7 1.7 0 0 0 0-3.4V8.6Z" />
      <path d="M14.4 7.6v1.9m0 2.6v1.9m0 2.6v1.4" />
    </>
  ),
  envelope: (
    <>
      <rect x="2.8" y="5" width="18.4" height="14" rx="2" />
      <path d="m3.4 6.8 7.7 5.4c.5.4 1.3.4 1.8 0l7.7-5.4" />
    </>
  ),
  chat: (
    <>
      <path d="M9.2 4h7.3c2 0 3.5 1.6 3.5 3.5v3.8c0 2-1.6 3.5-3.5 3.5h-.9l-3.4 2.7v-2.7H9.2a3.5 3.5 0 0 1-3.5-3.5V7.5C5.7 5.6 7.3 4 9.2 4Z" />
      <path d="M5.1 9.6H4a2.6 2.6 0 0 0-2.6 2.6v2.6c0 1.4 1.2 2.6 2.6 2.6h.4v2.4l2.9-2.4h3.4" />
    </>
  ),
  folderShare: (
    <>
      <path d="M2.8 6.6c0-.9.7-1.6 1.6-1.6h4.1l2 2.4h7.1c.9 0 1.6.7 1.6 1.6v1.4" />
      <path d="M2.8 8.8v8.6c0 .9.7 1.6 1.6 1.6h6.2" />
      <circle cx="19.2" cy="13.4" r="1.9" />
      <circle cx="14.4" cy="18.6" r="1.9" />
      <path d="m17.6 14.7-1.7 2.6" />
    </>
  ),
  browser: (
    <>
      <rect x="2.8" y="4.4" width="18.4" height="15.2" rx="2" />
      <path d="M2.8 9.2h18.4" />
      <path d="M5.9 6.8h.01M8.6 6.8h.01M11.3 6.8h.01" strokeLinecap="round" strokeWidth="1.9" />
    </>
  ),
  terminal: (
    <>
      <rect x="2.8" y="4.4" width="18.4" height="15.2" rx="2" />
      <path d="m6.9 9.3 3 2.7-3 2.7" />
      <path d="M12.4 15h4.7" />
    </>
  ),
  plug: (
    <>
      <path d="M9.3 2.8v4.1M14.7 2.8v4.1" />
      <path d="M6.4 6.9h11.2v3.6a5.6 5.6 0 0 1-5.6 5.6 5.6 5.6 0 0 1-5.6-5.6V6.9Z" />
      <path d="M12 16.1v5.1" />
    </>
  ),
  puzzle: (
    <path d="M10.3 3.4c1 0 1.8.8 1.8 1.8 0 .4-.1.7-.3 1h3.3c.6 0 1.1.5 1.1 1.1v3.1c.3-.2.6-.3 1-.3 1 0 1.8.8 1.8 1.9s-.8 1.9-1.8 1.9c-.4 0-.7-.1-1-.3v3.1c0 .6-.5 1.1-1.1 1.1H12c.2.3.3.6.3 1 0 1-.8 1.8-1.9 1.8s-1.9-.8-1.9-1.8c0-.4.1-.7.3-1H5.7c-.6 0-1.1-.5-1.1-1.1V7.3c0-.6.5-1.1 1.1-1.1h3.1a1.8 1.8 0 0 1-.3-1c0-1 .8-1.8 1.8-1.8Z" />
  ),
  code: (
    <>
      <path d="m8.4 8.2-4 3.8 4 3.8" />
      <path d="m15.6 8.2 4 3.8-4 3.8" />
      <path d="m13.4 5.4-2.8 13.2" />
    </>
  ),
  folder: (
    <path d="M2.8 6.6c0-.9.7-1.6 1.6-1.6h4.1l2 2.4h7.1c.9 0 1.6.7 1.6 1.6v8.4c0 .9-.7 1.6-1.6 1.6H4.4a1.6 1.6 0 0 1-1.6-1.6V6.6Z" />
  ),
  globe: (
    <>
      <circle cx="12" cy="12" r="9.2" />
      <path d="M2.8 12h18.4" />
      <path d="M12 2.8c2.3 2.5 3.6 5.8 3.6 9.2s-1.3 6.7-3.6 9.2c-2.3-2.5-3.6-5.8-3.6-9.2S9.7 5.3 12 2.8Z" />
    </>
  ),
  box: (
    <>
      <path d="M12 2.9 20.6 7v10L12 21.1 3.4 17V7L12 2.9Z" />
      <path d="M3.6 7.1 12 11.3l8.4-4.2M12 11.3v9.6" />
    </>
  ),
};

// Kategorie → Symbol. Die Kategorien kommen aus der API (target.Category…).
const byCategory: Record<string, string> = {
  ticketing: "ticket",
  code: "code",
  communication: "chat",
  files: "folder",
  web: "globe",
  dev: "terminal",
};

// Built-ins mit eigenem Symbol, wo kein freies Logo existiert.
const byName: Record<string, string> = {
  zammad: "ticket",
  email: "envelope",
  teams: "chat",
  sharepoint: "folderShare",
  browser: "browser",
  dev: "terminal",
  mcp: "plug",
};

const byKind: Record<string, string> = {
  mcp: "plug",
  custom: "puzzle",
};

export type TargetIconProps = {
  name: string;
  kind?: string;
  category?: string;
  /** Kantenlänge in px. */
  size?: number;
  className?: string;
};

/**
 * Logo bzw. Symbol eines Zielsystems. Rein dekorativ (aria-hidden) — der
 * Name steht im Markup immer daneben.
 */
export function TargetIcon({ name, kind, category, size = 18, className }: TargetIconProps) {
  const brand = brandMarks[name];
  const common = {
    width: size,
    height: size,
    viewBox: "0 0 24 24",
    className,
    "aria-hidden": true as const,
    focusable: "false" as const,
  };

  if (brand) {
    return (
      <svg {...common} fill={brand.color}>
        <path d={brand.path} />
      </svg>
    );
  }

  const key =
    byName[name] ??
    (kind ? byKind[kind] : undefined) ??
    (category ? byCategory[category] : undefined) ??
    "box";

  return (
    <svg
      {...common}
      fill="none"
      stroke="currentColor"
      strokeWidth={1.7}
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      {glyphs[key]}
    </svg>
  );
}

/** Ob für dieses Zielsystem ein echtes Marken-Logo vorliegt. */
export function hasBrandMark(name: string): boolean {
  return name in brandMarks;
}
