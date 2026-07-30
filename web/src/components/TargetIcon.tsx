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

// Marken-Signets. Herkunft der Pfade:
//   - GitLab, Nextcloud, Microsoft Teams, Microsoft SharePoint: Simple Icons
//     (CC0), unverändert übernommen. Die beiden Microsoft-Signets stammen aus
//     simple-icons@9 — spätere Versionen führen die Microsoft-Marken nicht
//     mehr; sie stehen hier nur zur Kennzeichnung des jeweiligen Zielsystems.
//   - Google Chrome: nachgezeichnet in den Markenfarben, weil das Signet
//     mehrfarbig ist und Simple Icons nur eine einfarbige Silhouette führt.
// Alle sind gefüllt — sie sollen als Logo lesbar sein, nicht als Symbol.
const brandMarks: Record<string, { title: string; node: ReactNode }> = {
  gitlab: {
    title: "GitLab",
    node: (
      <path
        fill="#FC6D26"
        d="m23.6004 9.5927-.0337-.0862L20.3.9814a.851.851 0 0 0-.3362-.405.8748.8748 0 0 0-.9997.0539.8748.8748 0 0 0-.29.4399l-2.2055 6.748H7.5375l-2.2057-6.748a.8573.8573 0 0 0-.29-.4412.8748.8748 0 0 0-.9997-.0537.8585.8585 0 0 0-.3362.4049L.4332 9.5015l-.0325.0862a6.0657 6.0657 0 0 0 2.0119 7.0105l.0113.0087.03.0213 4.976 3.7264 2.462 1.8633 1.4995 1.1321a1.0085 1.0085 0 0 0 1.2197 0l1.4995-1.1321 2.4619-1.8633 5.006-3.7489.0125-.01a6.0682 6.0682 0 0 0 2.0094-7.003z"
      />
    ),
  },
  nextcloud: {
    title: "Nextcloud",
    node: (
      <path
        fill="#0082C9"
        d="M12.018 6.537c-2.5 0-4.6 1.712-5.241 4.015-.56-1.232-1.793-2.105-3.225-2.105A3.569 3.569 0 0 0 0 12a3.569 3.569 0 0 0 3.552 3.553c1.432 0 2.664-.874 3.224-2.106.641 2.304 2.742 4.016 5.242 4.016 2.487 0 4.576-1.693 5.231-3.977.569 1.21 1.783 2.067 3.198 2.067A3.568 3.568 0 0 0 24 12a3.569 3.569 0 0 0-3.553-3.553c-1.416 0-2.63.858-3.199 2.067-.654-2.284-2.743-3.978-5.23-3.977zm0 2.085c1.878 0 3.378 1.5 3.378 3.378 0 1.878-1.5 3.378-3.378 3.378A3.362 3.362 0 0 1 8.641 12c0-1.878 1.5-3.378 3.377-3.378zm-8.466 1.91c.822 0 1.467.645 1.467 1.468s-.644 1.467-1.467 1.468A1.452 1.452 0 0 1 2.085 12c0-.823.644-1.467 1.467-1.467zm16.895 0c.823 0 1.468.645 1.468 1.468s-.645 1.468-1.468 1.468A1.452 1.452 0 0 1 18.98 12c0-.823.644-1.467 1.467-1.467z"
      />
    ),
  },
  teams: {
    title: "Microsoft Teams",
    node: (
      <path
        fill="#6264A7"
        d="M20.625 8.127q-.55 0-1.025-.205-.475-.205-.832-.563-.358-.357-.563-.832Q18 6.053 18 5.502q0-.54.205-1.02t.563-.837q.357-.358.832-.563.474-.205 1.025-.205.54 0 1.02.205t.837.563q.358.357.563.837.205.48.205 1.02 0 .55-.205 1.025-.205.475-.563.832-.357.358-.837.563-.48.205-1.02.205zm0-3.75q-.469 0-.797.328-.328.328-.328.797 0 .469.328.797.328.328.797.328.469 0 .797-.328.328-.328.328-.797 0-.469-.328-.797-.328-.328-.797-.328zM24 10.002v5.578q0 .774-.293 1.46-.293.685-.803 1.194-.51.51-1.195.803-.686.293-1.459.293-.445 0-.908-.105-.463-.106-.85-.329-.293.95-.855 1.729-.563.78-1.319 1.336-.756.557-1.67.861-.914.305-1.898.305-1.148 0-2.162-.398-1.014-.399-1.805-1.102-.79-.703-1.312-1.664t-.674-2.086h-5.8q-.411 0-.704-.293T0 16.881V6.873q0-.41.293-.703t.703-.293h8.59q-.34-.715-.34-1.5 0-.727.275-1.365.276-.639.75-1.114.475-.474 1.114-.75.638-.275 1.365-.275t1.365.275q.639.276 1.114.75.474.475.75 1.114.275.638.275 1.365t-.275 1.365q-.276.639-.75 1.113-.475.475-1.114.75-.638.276-1.365.276-.188 0-.375-.024-.188-.023-.375-.058v1.078h10.875q.469 0 .797.328.328.328.328.797zM12.75 2.373q-.41 0-.78.158-.368.158-.638.434-.27.275-.428.639-.158.363-.158.773 0 .41.158.78.159.368.428.638.27.27.639.428.369.158.779.158.41 0 .773-.158.364-.159.64-.428.274-.27.433-.639.158-.369.158-.779 0-.41-.158-.773-.159-.364-.434-.64-.275-.275-.639-.433-.363-.158-.773-.158zM6.937 9.814h2.25V7.94H2.814v1.875h2.25v6h1.875zm10.313 7.313v-6.75H12v6.504q0 .41-.293.703t-.703.293H8.309q.152.809.556 1.5.405.691.985 1.19.58.497 1.318.779.738.281 1.582.281.926 0 1.746-.352.82-.351 1.436-.966.615-.616.966-1.43.352-.815.352-1.752zm5.25-1.547v-5.203h-3.75v6.855q.305.305.691.452.387.146.809.146.469 0 .879-.176.41-.175.715-.48.304-.305.48-.715t.176-.879Z"
      />
    ),
  },
  sharepoint: {
    title: "Microsoft SharePoint",
    node: (
      <path
        fill="#038387"
        d="M24 13.5q0 1.242-.475 2.332-.474 1.09-1.289 1.904-.814.815-1.904 1.29-1.09.474-2.332.474-.762 0-1.523-.2-.106.997-.557 1.858-.451.862-1.154 1.494-.704.633-1.606.99-.902.358-1.91.358-1.09 0-2.045-.416-.955-.416-1.664-1.125-.709-.709-1.125-1.664Q6 19.84 6 18.75q0-.188.018-.375.017-.188.04-.375H.997q-.41 0-.703-.293T0 17.004V6.996q0-.41.293-.703T.996 6h3.54q.14-1.277.726-2.373.586-1.096 1.488-1.904Q7.652.914 8.807.457 9.96 0 11.25 0q1.395 0 2.625.533T16.02 1.98q.914.915 1.447 2.145T18 6.75q0 .188-.012.375-.011.188-.035.375 1.242 0 2.344.469 1.101.468 1.928 1.277.826.809 1.3 1.904Q24 12.246 24 13.5zm-12.75-12q-.973 0-1.857.34-.885.34-1.577.943-.691.604-1.154 1.43Q6.2 5.039 6.06 6h4.945q.41 0 .703.293t.293.703v4.945l.21-.035q.212-.75.61-1.424.399-.673.944-1.218.545-.545 1.213-.944.668-.398 1.43-.61.093-.503.093-.96 0-1.09-.416-2.045-.416-.955-1.125-1.664-.709-.709-1.664-1.125Q12.34 1.5 11.25 1.5zM6.117 15.902q.54 0 1.06-.111.522-.111.932-.37.41-.257.662-.679.252-.422.252-1.055 0-.632-.263-1.054-.264-.422-.662-.703-.399-.282-.856-.463l-.855-.34q-.399-.158-.662-.334-.264-.176-.264-.445 0-.2.14-.323.141-.123.335-.193.193-.07.404-.094.21-.023.351-.023.598 0 1.055.152.457.153.95.457V8.543q-.282-.082-.522-.14-.24-.06-.475-.1-.234-.041-.486-.059-.252-.017-.557-.017-.515 0-1.054.117-.54.117-.979.375-.44.258-.715.68-.275.421-.275 1.03 0 .598.263.997.264.398.663.68.398.28.855.474l.856.363q.398.17.662.358.263.187.263.457 0 .222-.123.351-.123.13-.31.2-.188.07-.393.087-.205.018-.369.018-.703 0-1.248-.234-.545-.235-1.107-.621v1.875q1.195.468 2.472.468zM11.25 22.5q.773 0 1.453-.293t1.19-.803q.51-.51.808-1.195.299-.686.299-1.459 0-.668-.223-1.277-.222-.61-.62-1.096-.4-.486-.95-.826-.55-.34-1.207-.48v1.933q0 .41-.293.703t-.703.293H7.57q-.07.375-.07.75 0 .773.293 1.459t.803 1.195q.51.51 1.195.803.686.293 1.459.293zM18 18q.926 0 1.746-.352.82-.351 1.436-.966.615-.616.966-1.43.352-.815.352-1.752 0-.926-.352-1.746-.351-.82-.966-1.436-.616-.615-1.436-.966Q18.926 9 18 9t-1.74.357q-.815.358-1.43.973t-.973 1.43q-.357.814-.357 1.74 0 .129.006.258t.017.258q.551.27 1.02.65t.838.855q.369.475.627 1.026.258.55.387 1.148Q17.18 18 18 18Z"
      />
    ),
  },
  // Chrome: die drei Kreissektoren (rot oben, grün links, gelb rechts),
  // darüber der weiße Ring und der blaue Kern — in dieser Reihenfolge
  // gezeichnet, die späteren Formen decken die früheren ab.
  browser: {
    title: "Google Chrome",
    node: (
      <>
        <path fill="#34A853" d="M12 12 12 23A11 11 0 0 1 2.474 6.5Z" />
        <path fill="#FBBC05" d="M12 12 21.526 6.5A11 11 0 0 1 12 23Z" />
        <path fill="#EA4335" d="M1.046 11A11 11 0 0 1 22.954 11Z" />
        <circle cx="12" cy="12" r="5.6" fill="#FFF" />
        <circle cx="12" cy="12" r="4.5" fill="#4285F4" />
      </>
    ),
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

// Built-ins mit eigenem Symbol, wo kein Signet vorliegt.
const byName: Record<string, string> = {
  zammad: "ticket",
  email: "envelope",
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
    return <svg {...common}>{brand.node}</svg>;
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
