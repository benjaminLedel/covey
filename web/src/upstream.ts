// Ein Befund, den ein Agent aufschreibt, muss von jemandem gemeldet werden, der
// darf. Ein Agent ohne Schreibzugang zum Tracker der Plattform ist der
// Normalfall und soll es bleiben: Issues anzulegen ist ein Schreibzugriff unter
// einer Identität, und die gehört nicht in eine Sandbox.
//
// Der Ausweg ist der vorbefüllte Link. GitHub und GitLab nehmen Titel und Rumpf
// als Query-Parameter entgegen und öffnen ihr Formular damit — der Mensch liest
// gegen, drückt ab, und die Meldung trägt seinen Namen. Kein Token, keine
// Freigabe, und vor allem: nichts wird veröffentlicht, ohne dass jemand es
// vorher gesehen hat. Bei Berichten, die Wiki-Auszüge und Aktionsprotokolle
// zitieren, ist das kein Detail, sondern der Grund für diese Lösung.

/** urlLimit ist der Punkt, an dem ein vorbefüllter Link aufhört zu
 *  funktionieren. Server und Browser setzen die Grenze verschieden (GitHub
 *  antwortet jenseits davon mit 414), und was zählt, ist die ganze URL — nicht
 *  der Rumpf allein. 6000 Zeichen liegen unter jeder Grenze, die uns begegnet
 *  ist, und tragen einen vollständigen Befund. */
const urlLimit = 6000;

export type UpstreamRepo = {
  /** Zielsystem: `github` oder `gitlab`. Alles andere hat kein bekanntes
   *  Formular und bekommt deshalb keinen Link. */
  system?: string;
  /** Projektpfad, z. B. `benjaminLedel/covey`. */
  project?: string;
};

/** issueBase ist die Adresse des Formulars, und sie unterscheidet sich je
 *  System — nicht nur im Pfad, auch in den Namen der Parameter. */
function issueBase(system: string, project: string): { url: string; title: string; body: string } | null {
  switch (system) {
    case "github":
      return { url: `https://github.com/${project}/issues/new`, title: "title", body: "body" };
    case "gitlab":
      // Die Instanz steht nicht fest: ein selbstgehostetes GitLab hat einen
      // eigenen Host, den wir hier nicht kennen. gitlab.com ist die einzige
      // Adresse, die ohne weiteres Wissen richtig ist — für alles andere
      // liefert diese Funktion lieber nichts als einen Link ins Leere.
      return { url: `https://gitlab.com/${project}/-/issues/new`, title: "issue[title]", body: "issue[description]" };
    default:
      return null;
  }
}

/** truncateBody kürzt den Rumpf so, dass die fertige URL unter urlLimit bleibt,
 *  und sagt im Text, dass gekürzt wurde. Ein stillschweigend abgeschnittener
 *  Befund ist schlimmer als ein sichtbar unvollständiger: der erste wird
 *  geglaubt. */
function truncateBody(body: string, overhead: number, note: string): string {
  const room = urlLimit - overhead;
  if (encodeURIComponent(body).length <= room) return body;
  // Die Kodierung bläht auf (ein Zeilenumbruch wird zu drei Zeichen), also wird
  // gemessen statt gerechnet: halbieren, bis es passt, dann feiner.
  let text = body;
  while (text.length > 0 && encodeURIComponent(text + note).length > room) {
    text = text.slice(0, Math.floor(text.length * 0.9));
  }
  return text + note;
}

/** upstreamIssueURL baut den vorbefüllten Link. null = für dieses Ziel gibt es
 *  keinen (kein Repo eingerichtet, oder ein System ohne bekanntes Formular);
 *  der Aufrufer zeigt dann keinen Knopf, statt einen toten anzubieten. */
export function upstreamIssueURL(opts: {
  repo: UpstreamRepo;
  title: string;
  body: string;
  /** truncationNote steht im Rumpf, wenn gekürzt wurde — übersetzt vom
   *  Aufrufer, weil dieser Text der Mensch liest. */
  truncationNote?: string;
}): string | null {
  const system = (opts.repo.system ?? "").trim().toLowerCase();
  const project = (opts.repo.project ?? "").trim();
  if (!system || !project || project === "-") return null;
  const base = issueBase(system, project);
  if (!base) return null;

  const title = opts.title.trim().slice(0, 200);
  const overhead = base.url.length + base.title.length + base.body.length + encodeURIComponent(title).length + 4;
  const body = truncateBody(opts.body.trim(), overhead, opts.truncationNote ?? "\n\n…");
  const params = new URLSearchParams();
  params.set(base.title, title);
  params.set(base.body, body);
  return `${base.url}?${params.toString()}`;
}
