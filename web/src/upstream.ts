// A finding that an agent writes down has to be reported by someone who is
// allowed to. An agent without write access to the platform's tracker is the
// normal case and should stay that way: creating issues is a write access
// under an identity, and that does not belong in a sandbox.
//
// The way out is the prefilled link. GitHub and GitLab accept title and body
// as query parameters and open their form with them — the human reads it over,
// presses the button, and the report carries their name. No token, no
// approval, and above all: nothing gets published without someone having seen
// it beforehand. For reports that quote wiki excerpts and action logs this is
// not a detail, but the reason for this solution.

/** urlLimit is the point where a prefilled link stops working. Servers and
 *  browsers set the limit differently (GitHub answers beyond it with 414), and
 *  what counts is the whole URL — not the body alone. 6000 characters lie
 *  under every limit we have met so far, and they carry a
 *  complete finding. */
const urlLimit = 6000;

export type UpstreamRepo = {
  /** Target system: `github` or `gitlab`. Anything else has no known
   *  form and therefore gets no link. */
  system?: string;
  /** Projektpfad, z. B. `benjaminLedel/covey`. */
  project?: string;
};

/** issueBase is the address of the form, and it differs by system — not only
 *  in the path, also in the names of the parameters. */
function issueBase(system: string, project: string): { url: string; title: string; body: string } | null {
  switch (system) {
    case "github":
      return { url: `https://github.com/${project}/issues/new`, title: "title", body: "body" };
    case "gitlab":
      // The instance is not fixed: a self-hosted GitLab has its own host,
      // which we do not know here. gitlab.com is the only address that is
      // right without further knowledge — for anything else this function
      // returns nothing rather than a link that leads nowhere.
      return { url: `https://gitlab.com/${project}/-/issues/new`, title: "issue[title]", body: "issue[description]" };
    default:
      return null;
  }
}

/** truncateBody shortens the body so the finished URL stays under urlLimit, and
 *  says in the text that it was shortened. A finding silently cut off is worse
 *  than a visibly incomplete one: the first one gets
 *  believed. */
function truncateBody(body: string, overhead: number, note: string): string {
  const room = urlLimit - overhead;
  if (encodeURIComponent(body).length <= room) return body;
  // The encoding inflates (one line break becomes three characters), so it is
  // measured instead of computed: halve until it fits, then finer.
  let text = body;
  while (text.length > 0 && encodeURIComponent(text + note).length > room) {
    text = text.slice(0, Math.floor(text.length * 0.9));
  }
  return text + note;
}

/** upstreamIssueURL builds the prefilled link. null = for this target there is
 *  none (no repo set up, or a system without a known form); the caller then
 *  shows no button instead of offering a dead one. */
export function upstreamIssueURL(opts: {
  repo: UpstreamRepo;
  title: string;
  body: string;
  /** truncationNote stands in the body when it was shortened — translated by
   *  the caller, because this text is what the human reads. */
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
