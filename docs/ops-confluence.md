# Operations: connecting Covey to Confluence

A practical runbook for the target system **Confluence**
(`github.com/benjaminLedel/covey-plugin-pack/confluence`). The unit is the
**page**; the plugin covers Confluence **Cloud** and **Server/Data Center**.

> Short version: an API token (Cloud) or a personal access token (Data Center),
> stored as `confluence_token` + `confluence_url`. The agent reads pages as
> **Markdown** and writes them as Markdown — the storage format Confluence
> keeps them in never reaches it. There is **no intake**: Confluence wakes
> nobody. These are **configuration steps**, not a rebuild.

## What this is for, and what it is not

Confluence is the documentation the other systems hang off. The specification a
Jira ticket links to, the runbook a merge request invalidates, the release note
somebody looks for next quarter. An agent reads it **while** working on
something else, and writes to it **when** that work is done.

Two boundaries are worth drawing before the first token is created:

- **It is not a source of work.** Nobody is assigned a page. There is no
  `HEARTBEAT.md` entry for this system and no `nur-wenn: confluence` — the
  plugin has no work check at all, and a test in the pack asserts that it never
  grows one by accident. Give the agent Jira or GitLab beside it, or it has no
  occasion to look here.
- **It is not Covey's wiki memory.** Covey has one of its own
  ([`../spec/05-memory.md`](../spec/05-memory.md)): linked Markdown pages with a
  `pgvector` index, private to the agent, curated for retrieval. That is the
  agent's *memory*. Confluence is the *company's* documentation — shared with
  humans, with its own permissions and its own history. An agent uses the first
  to remember and the second to be understood.

## Cloud or Data Center

One plugin, two deployments, and they are further apart than Jira's:

| | Confluence Cloud | Server / Data Center |
|---|---|---|
| `confluence_token` | `mail@example.com:<API token>` | `<personal access token>` |
| HTTP auth | Basic | Bearer |
| Base path | `https://acme.atlassian.net/wiki` | `https://confluence.acme.example` |
| Pages | REST **v2** (`/api/v2/pages`) | REST **v1** (`/rest/api/content`) |
| Search | REST **v1** (`/rest/api/content/search`) — CQL never moved to v2 | REST v1 |
| A space is named by | a numeric id (v2) | its key |

A pair with a colon is Cloud, a single value is a personal access token. The
`/wiki` context path exists only in the Cloud, and the plugin appends it when it
is missing — the browser hides it, so nobody has it in hand. Where the inference
is wrong, write it out:

```
confluence_url = https://confluence.acme.example auth=bearer api=1
```

---

## 1. Step-by-step instructions

### 1.1 In Confluence: an account and a token

Give the agent **a user of its own** (`covey-bot`) with access to the spaces it
is to work. Every page version and every comment carries that name, and a reader
of the page history should be able to tell an agent's edit from a colleague's.

**Cloud:** log in as that user → `id.atlassian.com` → *Security → API tokens*.
It is the **same kind of token Jira uses** — one Atlassian account, two
products, two secrets in Covey. If the agent already works Jira, this is the
same account and can be the same token value.

**Server/Data Center:** as that user → *Profile → Personal Access Tokens*.

### 1.2 In Covey: deposit the secrets

| Secret | Value |
|---|---|
| `confluence_url` | `https://acme.atlassian.net/wiki` (or without `/wiki` — it is appended) |
| `confluence_token` | `covey-bot@acme.example:<API token>` / `<PAT>` |

Optional components after the URL, separated by spaces:

```
confluence_url = https://acme.atlassian.net/wiki space="ENG" api=2 auth=basic
```

**`space=` is a boundary, not a default.** An agent whose credential names a
space reads and writes that space and no other — through `search`, through
`get_page` with an id somebody quoted at it, and through everything that writes.
Several: `space="ENG,OPS"`.

Note what it costs, because it differs from Jira's project wall: a Jira key
carries its project in front of the hyphen, so `ACME-17` can be judged without
asking anybody. **A Confluence page id carries nothing at all**, so the space is
read before the page is touched — free on a read (the page is fetched anyway),
one call on a write. A wiki is exactly the system where somebody wants that
assurance in writing, so it is worth the call.

The wall is applied to a **search** by bracketing the agent's own query:

```
your query:   type = page AND title ~ "runbook" ORDER BY created
what is sent: space in (ENG) AND (type = page AND title ~ "runbook") ORDER BY created
```

### 1.3 In Covey: enable the target system

```markdown
- system: confluence scope: read,write,comment
```

| Scope | What it permits |
|---|---|
| `read` | `search`, `get_page`, `list_children`, `list_spaces`, `list_comments`, `list_attachments`, `download_attachment` |
| `comment` | `comment` |
| `write` | `append_to_page`, `update_page`, `create_page`, `add_labels`, `attach_file` |

**Read-only is a real option here.** An agent that pulls the specification into
its context and writes nothing needs `scope: read`, and that is a defensible
setup for the first weeks. The prompt documentation is narrowed to the scopes
granted, so a read-only agent does not carry the writing procedure through every
turn.

### 1.4 Testing

The connection test on the plugin page names the account, the deployment and the
wall:

```
Covey Bot (covey-bot@acme.example) · Cloud · ENG
```

Read all three. The deployment is an inference from the token's shape, and this
is the one place somebody can see that it is wrong.

---

## 2. Appending versus replacing

This is the section to read before granting `write`.

```json
append_to_page {"page_id":"131075","version":7,"message":"release 1.2",
                "body":"## Release 1.2\n\nThe importer guards the null case now — ACME-17."}
```

**`append_to_page` is the one an agent normally wants.** Almost everything an
agent writes to a wiki is an addition — a release note, a finding, a line in a
runbook. Appending cannot lose what somebody else wrote, and the existing body
is written back **untranslated**: rendering it to Markdown and back would
reformat everything a human wrote, and a diff in which the whole page moved is a
diff nobody reviews.

`update_page` replaces the entire body. It is the right action when the page
really is the agent's to rewrite, and the wrong one otherwise. The two are
**separate guard-rail subjects** (`confluence:append_to_page` and
`confluence:update_page`) precisely so that an organisation can permit the first
and hold the second for approval:

| Rule | Effect |
|---|---|
| `confluence:update_page` → *ask* | every full rewrite goes through the Approvals page |
| `confluence:update_page` → *deny* | the agent can only ever add |

### The version number

Confluence numbers every revision and refuses a write that is not exactly one
ahead. That sounds like protection and is not: a plugin that reads the current
number and increments it will happily overwrite an edit made in between.

So **the agent passes the version it read** — the number `get_page` returned:

```
page stands at 7, agent read 7  → write goes through, page is now 8
page stands at 8, agent read 7  → refused: "somebody wrote in between"
```

Without it the last write wins, and the result says so. That is not hidden
because a page nobody else touches is the common case, and demanding a version
there would cost a call and teach the agent a step it forgets.

`message` becomes the version comment in the page history. Somebody will read it
before they read the diff.

---

## 3. What the agent sees instead of storage format

A page is stored as an XHTML derivative with Atlassian's own elements woven
through it — `<ac:structured-macro>` for a code block, `<ac:link>` around an
`<ri:page>` for a link to another page, `<ac:image>` around an `<ri:attachment>`
for a picture. The plugin renders it to Markdown on the way in and builds it
back on the way out.

What survives the round trip: headings, paragraphs, bullet and numbered lists,
**task lists** (Confluence's real checkboxes, from `- [x]`), fenced code blocks
with their language, block quotes, tables, links, inline code, bold, italics.

What does not: a table comes back as Markdown-ish rows and goes back as a
paragraph, panels become `[warning] …`, and a macro whose output only the server
knows — a page tree, an included Jira filter — becomes `[pagetree macro]` rather
than a silent gap. The sentence is what matters; a formatting that was not
recognised is a smaller loss than a page that does not get written.

**The agent writes Markdown.** It should never be asked to produce storage
format itself: the result is almost-XHTML, a 400 it cannot learn from, and a
retry with a slightly different tree.

---

## 4. The three systems, one loop

```
Jira            ACME-17 "Importer drops rows"
  │                links → Confluence "Import pipeline" (the spec)
  ▼
Confluence      get_page {"title":"Import pipeline"}      ← read BEFORE coding
  │
GitLab          checkout · branch ACME-17-… · commit "ACME-17 …" · MR
  │
Confluence      append_to_page  (release note, runbook correction)
  │
Jira            comment (the MR link) · transition
```

Two habits worth putting into the agent's `PLAYBOOKS.md`:

1. **Read the linked page before writing code.** The ticket's summary is a
   headline; the requirement is on the page. `get_page` takes a title, which is
   what a Confluence link shows.
2. **Write down what you had to find out.** The runbook that was wrong, the
   parameter nobody had documented — that is a section, appended, with the issue
   key in the text so the two can be found from each other.

---

## 5. Attachments

```json
list_attachments {"page_id":"131075"}
download_attachment {"page_id":"131075","name":"architecture.png"}
```

Addressed by **name**, not by id: an attachment id is not something an agent
has, while the name stands in the page it just read (`[attachment:
architecture.png]`). A name that is not there is answered with the ones that
are.

The way back is `attach_file`; note that the upload alone puts nothing in the
page — a `append_to_page` that mentions the file is what makes anybody find it.

---

## 6. Env reference (Confluence-relevant)

| Variable | Default | Effect |
|---|---|---|
| `COVEY_CONFLUENCE_INTAKE_SPACES` | *(empty)* | allowlist of space keys the plugin may write to. Empty = every space. Installation-wide; the per-agent wall is `space=` in `confluence_url`. |
| `COVEY_CONFLUENCE_ATTACHMENT_MAX_MB` | `25` | per file, in both directions (1…1024). |

There is no `COVEY_CONFLUENCE_WEBHOOK_SECRET`, because there is no webhook —
Confluence Cloud has none an admin can simply enter; that needs a Connect/Forge
app.

---

## 7. Troubleshooting

| Symptom | Cause | Remedy |
|---|---|---|
| `HTTP 404` on every call | the `/wiki` path missing or doubled, or a Cloud site addressed as v1 | give the site URL; the plugin appends `/wiki` itself for a Cloud credential |
| `HTTP 401` right at the connection test | Cloud token used without the mail address, or a PAT sent as Basic | `mail:token` for Cloud, the bare token for Data Center; `auth=` writes it out |
| `page … lies outside your spaces` | the per-agent wall | intended — widen `space=` only if the agent really is to work there |
| `Version must be incremented` / 409 | two writes raced | the agent should pass the `version` from `get_page`; then it gets the readable error instead |
| A page comes back with `[pagetree macro]` in it | a macro the server renders | expected — the content is not in the page, it is generated on view |
| Markdown appears literally in the page | the body was sent to a system that is not this plugin | the plugin always translates; check that the action really was `confluence/*` |
| The agent never opens Confluence | no occasion | it is not a source of work — the playbook has to say when to look |

---

## See also

- [`ops-jira.md`](ops-jira.md) — the ticket, and the same Atlassian account
- [`ops-gitlab.md`](ops-gitlab.md), [`ops-github.md`](ops-github.md) — the code
- [`../spec/05-memory.md`](../spec/05-memory.md) — Covey's own wiki memory, and
  why it is a different thing
