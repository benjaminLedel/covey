# 24 — Voice: an author's style as an object an agent carries

**Status: slices 1 to 3 are built, the guided way to build a voice (#458), and voices per occasion (#471).** Slice 1 lives in the covey-style skill, slices 2 and 3 in covey (#195, #257).

The style gate ([`06-observability-control.md`](06-observability-control.md)) holds an agent's outgoing text inside bands measured from a corpus. The bands say *how far* a text is from the corpus; they cannot make the text sound like the corpus's author. This document describes what does, and how it becomes an object in covey.

## Why bands are not enough

On covey.work the writing agents carried a profile built from the site's own August posts. Those posts were AI text; the owner found them all artificial, and measured against texts people wrote — twelve Hacker News front-page blog posts, fourteen WinFuture articles, the owner's own three documents — the agent drafts fell out on the same metrics in every corpus:

| Metric | agent draft | people |
|---|---|---|
| paragraphs without a name, number, example or link | 56 to 59 % | 1 to 38 % |
| mean sentence length | 12 to 13 words | 14 to 24 |
| paragraph length variation (sd/mean) | 0.42 | 0.47 to 1.2 |
| antithesis closers per 1000 words ("das ist Zeit, kein Datenverlust") | 5.6 to 8.6 | 0 to 1.6 |
| dashes per 1000 words (the AI August posts) | 7 to 19 | 0 to 1 |

A model satisfies "sentences of 13 to 20 words, no dashes" without sounding like anybody. What transfers a voice is examples of it in the prompt, a description in words of what the author does and never does, and the profile as the guard behind both — all three, none alone.

## What a voice is

A **voice** is an organisation-level object, versioned, built once from texts a person uploads — or written from a description, see [Building a voice](#building-a-voice) — and assigned per occasion to an agent, a department or the organisation, see [Voices per occasion](#voices-per-occasion-471). Four artefacts:

1. **Profile** — the ```style-profile``` block as in [`02-agent-model.md`](02-agent-model.md): bands per metric, the lexicon, plus `antithesis_rate`. The guard. Address metrics (wir, ich, Sie, du, man, questions) leave the profile by default: a tender document says "Sie" and never "wir", a blog by the same author may say both; they belong to the register of the corpus, not to the author's hand.
2. **Exemplars** — five to eight paragraphs chosen from the corpus for variety: an opening, one that carries evidence, one with an example, a long one, a short one, a closing. They go into the compiled prompt under `## Tone`. A paragraph that closes on an antithesis is never an exemplar; the same paragraph reused in two documents counts once.
3. **Style card** — 250 to 400 words in the corpus's language: how the author opens, carries an argument, holds on to facts, builds sentences and paragraphs, and what they never do. Written once by the organisation's model from exemplars and measurements, every claim tied to a quoted passage of at most eight words; a claim without a passage is left out. A person releases the card; a released card is not rewritten by the next build.
4. **Contrast list** — the metrics on which a reference corpus of AI text sits a full band width outside the author's band, in words: "dashes per 1000 words: the author never (0.0); a model 10.3". "Never" is the sharpest signal a corpus yields, and it falls out of the comparison on its own. covey ships the reference corpus.

Knowledge and experience are not part of a voice. They come from the model and from covey's own material — the wiki, the recording, the specification; a text without material sounds like a model in any voice. The measured lever for that is the anchor metrics, not the card.

## Where each artefact acts

| Moment | What acts | Where |
|---|---|---|
| writing | exemplars and card in the prompt | the run's `TONE.md`, rendered at dispatch from the voice of the task's occasion; `agents.CompilePrompt`, the `## Tone` section. In the team chat: card and two passages in the triage and the narration (`voice.ChatPrompt`) |
| revising | card as the editor rules, exemplars beside the findings | `covey/style_apply` ([`06`](06-observability-control.md)), with the voice of the task's outward occasion |
| leaving | profile bands and the contrast list as findings with evidence | the style gate, with the voice of the task's outward occasion |

The gate keeps acting on HIGH findings only; the contrast list turns the author's "never" into bands whose upper edge is close to zero, so a single dash is a MEDIUM and a page of them a HIGH.

## The tone in the team chat (#457)

A fifth part of a voice is set rather than built: how an agent carrying it talks in the team chat ([`28`](28-team-surface.md), section 5). Whether a colleague says "du" or "Sie", and whether a thank-you gets an emoji, is in no corpus — the corpus is written for the record — and it is a decision of the organisation, like a dress code. Four fields:

- `address` — `du`, `sie`, or `auto` (the way the person writes; the organisation's `du` or `sie` when that cannot be told),
- `tone` — `casual`, `matter_of_fact` or `formal`,
- `emoji` — `never`, `sparingly` or `freely`,
- `note` — free text for what the three do not say, at most 300 characters.

The organisation has the same four as its default (`organizations.chat_tone`): it applies where no voice brings a tone, and field by field to what a voice leaves empty; a voice's own note replaces the default note rather than adding to it. Stored as `voices.chat_tone` and `organizations.chat_tone` (migration 0120), set with `PUT /api/v1/voices/{id}/chat-tone` and `PATCH /api/v1/org/chat-tone` by the same roles that change a voice; every role reads it.

The tone that acts is the one of the voice chosen for the chat ([below](#voices-per-occasion-471)); while no level names a chat voice, it is the tone of the agent's customers voice — the one voice an agent carried before #471, whose tone acted in the chat and keeps doing so. It acts where the chat is written — the triage and the narration, as one short block ("How you talk in the team chat: …") — and not in a run: what a run writes into a target system keeps the register of its voice, and changing the chat tone writes no config version.

A sixth part is set too: how the agents carrying the voice **sound** when their words are spoken (#497). In a call the Mac app speaks the agent's replies ([`27`](27-mobile-app.md)); the spoken voice belongs to how the agent speaks, so it sits here beside the chat tone. There is one source and no second: the organisation's **voice provider**, an OpenAI-compatible speech server ([`27`](27-mobile-app.md#the-voice-provider-497-498)), educa AI by default; the Mac's own synthesis speaks only while there is none or it cannot be reached. So the spoken voice is what the provider is told: `voices.speech` (migration 0125) holds `voice` (the voice's name at the provider, at most 100 characters; empty uses the organisation's default voice), `instructions` (a short English style hint, at most 300 characters, whitespace folded; empty derives one from the chat tone, below) and `speed` (0.7–1.3, the range the provider honours; 0 or absent means the voice's own). NULL — or a stored value of another shape — leaves the provider's default voice. It is set with `PUT /api/v1/voices/{id}/speech` by the roles that change a voice (an empty body clears it), and refused outside those bounds. The voice page's preview plays a sample sentence through `POST /speech/synthesize` in the browser.

The app reads it per conversation: `GET /api/v1/conversations/{id}/speech?agent=<agent id>` answers `{"speech": {…} | null, "voice": {"id", "name"} | null, "level": "department" | "agent" | "org" | "none", "instructions": "…", "address": "du" | "sie" | ""}` — the chat voice the rule [below](#voices-per-occasion-471) chooses for that agent with the caller as the addressed person, that voice's speech, and the style line the provider is told: the voice's own `instructions`, else one derived from the chat tone in effect and the voice's language ("casual and friendly, concise; speak German"). `address` is the chat tone's du or Sie for the call's greeting (#506), which is said before the person has written anything an `auto` could follow: `auto` takes the organisation's du or Sie behind it, and nothing set leaves it empty. Only a member of the conversation may ask, and only about an agent in it (404 otherwise). The call's greeting written from the situation (`POST /api/v1/conversations/{id}/greeting`, #513) takes its address and tone from the same resolution; it is described with the call in [`27`](27-mobile-app.md#the-greeting-506-513).

## Voices per occasion (#471)

One voice cannot fit both a customer mail and a quick answer to a colleague, and the person spoken to matters as much as the occasion: sales wants what a thing means for the customer, engineering the ticket and the branch. So a voice is assigned per **occasion**, and at three **levels**.

**Occasions** — three, and no more, because each has a different reader:

| Occasion | What is written | Who writes it |
|---|---|---|
| `chat` | the team chat | the triage and the narration ([`28`](28-team-surface.md)), and the run of a task that came from a conversation — its result is read out there |
| `customers` | outward: mails, tickets, replies in target systems | every other run |
| `publications` | blog, docs, offers | a run whose task carries a line `occasion: publications` (a `HEARTBEAT.md` `aufgabe:` can say it too) |

`voice.TaskOccasion` decides a run's occasion in that order: from a conversation it is `chat`; otherwise an explicit `occasion:` line; otherwise `customers`. The platform cannot tell a blog post from a ticket reply by looking at a task, so a publication has to say so. A text that leaves through a target system during a chat task — a mail sent from a conversation — still goes to a customer: its occasion for the style gate and the style services is `customers` (`voice.OutwardOccasion`).

**Resolution.** At the moment of writing, the first of these that names a voice for the occasion applies (`voice.Choose`, a rule without the database so it is tested as a rule):

1. the addressed person's **department** × occasion,
2. the **agent** × occasion,
3. the **organisation** × occasion.

The addressed person is the one who wrote the message, or who asked for the task (a task's origin `chat:<address>`). A customer has no department: levels 2 and 3. In a **group** whose people belong to more than one department — the addressed person and those who wrote among the conversation's last eight messages — level 1 is skipped: no one department's voice is right for all of them, and the agent's applies. A voice that is not assignable (any more) counts as none; a deleted voice leaves an empty slot, which falls to the next level. When no level names one, a run keeps the config's own `TONE.md` and the chat speaks without a voice block — what every agent without a slot has carried all along.

**The department's line.** A department has a short "how to speak with us" note (`departments.audience_note`, at most 400 characters, whitespace collapsed; the column checks it). It is added to what an agent writes to somebody of that department **whichever voice applies** — in the triage, the narration and the run of a conversation task, never to a customer. In a group the lines of every department involved come along, the addressed person's first, each department once, at most four lines and 1200 characters together; one line reads "How the Sales department wants to be spoken to (set by your organisation): …", several are introduced as "write so that it works for all of them". The turns are told that the organisation set these lines and to follow them.

**Reasons.** Every choice has a reason in one short form, the same in the recording, in a chat message's meta and in the table below: `department:<name>×<occasion>`, `agent×<occasion>`, `org×<occasion>`, and `none×<occasion>` when no level names one. A run records a lifecycle event with `voice_occasion`, `voice_reason`, `voice` and `voice_id` when there is one, `tone: the config's own TONE.md` when there is not, and `audience` (the departments whose lines came along); a chat answer carries `voice`, `voice_id`, `voice_reason` and `audience` in its meta, and the message head shows "voice: X · for Sales". "Why did it write like that" has an answer in the run and in the thread.

**What a slot is.** A slot is the assignment, and it acts at the moment of writing: the dispatch renders the chosen voice as the run's `TONE.md` from the voice as it stands then (`voice.Render`), and the chat reads the voice's released card and two passages (`voice.ChatPrompt`, bounded). Setting a slot therefore writes no config version — the change is in the slot table and every run records what it wrote in. Only a released card and the passages an agent reads act, never a draft. The style gate, `covey/style_check` and `covey/style_apply` measure against the profile of the voice the task's outward occasion resolves to, followed by the profiles in the config's other Markdown files, and fall back to the config's `TONE.md` only when no level names a voice ([`06`](06-observability-control.md)). Correction pairs from the approval gate go to the agent's `customers` voice, or its `publications` voice for an agent that only publishes: a rewritten text went out.

**Storage** — `voice_assignments` (migration 0122): one table for the three levels, `(org_id, agent_id | department_id | neither, occasion, voice_id)`, one voice per holder and occasion, cascading from the agent, the department and the voice. A row with neither holder is the organisation's default.

**API.** Every role reads; the manage roles (`org_admin`, `agent_owner`) change.

- `GET /api/v1/agents/{id}/voices` — `slots` (the agent's own, per occasion, `{voice_id, voice}` or null) and `effective` (what each occasion resolves to with nobody known: `voice_id`, `voice`, `level`, `reason`).
- `PUT /api/v1/agents/{id}/voices {"chat": "<id>", "customers": "", "publications": null}` — replaces the agent's slots; an occasion left out, `""` or `null` is empty. An unknown occasion, a malformed id or a voice that is not assignable is 400.
- `GET /api/v1/org/voices`, `PATCH /api/v1/org/voices` — the organisation's defaults; a PATCH changes only the occasions it names.
- `PATCH /api/v1/departments/{id}/audience {"audience_note": "…"}` — the line; longer than 400 characters is a 400.
- `PUT /api/v1/departments/{id}/voices` — the department's slots, replaced as for an agent. `GET /api/v1/departments` carries `audience_note` and `voices`.
- `GET /api/v1/voices/assignments[?agent_id=]` — **who gets what**: a row per department and one for everybody without (customers, people the org chart has not placed), a cell per occasion with the voice in effect, its level and reason. Without `agent_id` level 2 does not count.
- `GET /api/v1/voices/{id}` carries `used_by`: every agent, department and organisation default that names the voice, per occasion.
- `POST /api/v1/voices/{id}/preview` takes `audience` (a department id): the sample is written to somebody of it, with its line.
- `PUT /api/v1/agents/{id}/voice {"voice_id"}` — the one-voice assignment from before #471 — stays: it fills the `customers` and `publications` slots and still writes the `TONE.md` as a config version, which is what acts when no level names a voice.

In the interface: the agent's settings have one slot per occasion, and an empty slot says what applies instead and from where; the administration page sets the organisation's defaults; the org chart's edit mode opens a department's line (counted) and its voices; the voices list shows who gets what, optionally with one agent's slots counted; a voice's page lists who names it for what.

**Upgrade.** Migration 0122 copies every `agents.voice_id` into that agent's `customers` and `publications` slots and leaves the `chat` slot empty on purpose. Until then the team chat read only the voice's chat tone, never its card or passages; with an empty chat slot the chat falls back to the organisation's chat default, and with none to no voice block — while the chat *tone* still comes from the customers voice (see [the tone in the team chat](#the-tone-in-the-team-chat-457)). Nobody's colleague starts sounding like a tender. `agents.voice_id` stays as a column that nothing reads any more; the one-voice endpoint keeps writing it so that a binary rolled back past 0122 finds what it expects, and dropping it is a later migration's job.

## Building a voice (#458)

The rules above — four texts, one register, a card a person releases — used to stand in this document only; a person learned them from a weak build. The voices page now walks through six steps, and an existing voice shows the same six as sections of its page: **purpose, source, material, chat tone, preview, release**. A rail marks each step as done, open, optional or held up, and the line under it names what blocks the next one. The rule for that lives in one place (`web/src/pages/voices/flow.ts`) with its own tests: a "Next" that leads into a dead end teaches people to stop trusting it.

**Purpose** — `blog`, `support_mail`, `chat`, `offers` or `other`, stored as `voices.purpose`. It goes into the card prompt, the description prompt and the preview, and picks the preview's default kind (a mail for support and offers, a chat message for chat, a paragraph otherwise).

**Three sources**, one column: `voices.source` is `texts` or `described` (migration 0121).

- **From texts** — the build above. While the texts go in, the page shows checks from the same measurements the build uses (`voice.CheckCorpus`), and they claim only what a measurement can tell: how many more texts of 150 words or more are needed for a percentile spread (four in all); which texts are below 150 words and carry no band; which address the reader differently from the rest — "Sie" where the others say "du", read from `sie_per_1000` and `du_per_1000`, with a text that addresses nobody counted as neutral; which are in another language; and whose mean sentence length is more than 1.6 times off the others' median. Only a missing corpus blocks the build. Whether a text is *good* is not among the checks — nothing here can tell.
- **From a description** — a person says in plain words how the organisation writes. One call of the organisation's model (best tier, at most 4000 tokens, JSON answer) writes the card in the same shape and length as a corpus card, five to eight exemplar paragraphs with their roles, and a suggested chat tone. The one rule that differs is the evidence: a measured card quotes passages, a described one can rest only on the description, and the prompt tells the model to say where the description is silent rather than invent a habit. The parser is tolerant of fences and prose around the JSON, caps the exemplars at eight, and drops a chat-tone value that is not one of the choices field by field instead of failing the answer. A measured voice is not described over (409): its exemplars are quotes and its profile is what the gate checks.
- **From the chat** — an agent asked in the team chat drafts the voice with `covey/voice_draft {name, purpose, language, description | texts}`. What arrives is a draft: `voices.drafted_by` names the agent, nobody carries it, no card is released, and there is no op to release, build or assign — those stay on the voices page with the manage roles. No model call happens in the action; a person writes the card from the description, or builds from the texts, with one click whose cost is theirs. Its own guard-rail subject, `covey:voice_draft`, and every draft is in the recording.

**Nothing a model wrote acts before a release.** For a measured voice that was the card alone; a described voice's exemplars are model-written too, so they wait in `voices.draft_exemplars` and are released with the card. The suggested chat tone waits in `voices.suggested_chat_tone`: a chat tone acts as soon as it is set, so it is shown in the tone step and applies once somebody saves it. A described voice can be assigned only once it has been released.

**The gate and a described voice** (decided in #458). A voice from a description carries **no profile**: nothing was measured, and a band invented from a description would be a number with a straight face. Its `TONE.md` says "described in words rather than measured from texts" and has no ```style-profile``` block, so the style gate finds no profile, records `skipped` with the reason "the agent's voice is described, not measured", and lets the text pass. `style_apply` reads such a `TONE.md` whole as the prose to revise against. The voice acts while writing, through its card and exemplars, and the page says so. Texts added and built make it measured: `SaveBuild` sets `source = texts`, the exemplars are quotes again, the gate applies, and a released card stays.

**Preview** — `POST /api/v1/voices/{id}/preview {topic, kind, version}` writes one short sample (a paragraph, a mail, a chat message) in the voice: card, exemplars and, for a chat message, the effective chat tone. Fast tier, at most 700 tokens, stored nowhere. `version` is `draft` (the default) or `released`, so a refinement can be heard before and after.

**Refine by prompt** — `POST /api/v1/voices/{id}/refine {instruction}` revises the card, and a described voice's exemplars with it, by an instruction ("less formal, more concrete numbers"). The result is the draft (`voices.card`); the answer carries the text before, and the page shows the two side by side. It acts only on release, under the rule every card follows: a released card is not overwritten by a rebuild or a refinement.

API beside the existing routes: `PATCH /api/v1/voices/{id}` (purpose), `POST …/describe`, `…/preview`, `…/refine`; `POST /api/v1/voices` takes `purpose`; `GET /api/v1/voices/{id}` returns `checks` and `assignable`. RBAC as for the rest of a voice: every role reads, the manage roles change — the preview included, which changes nothing but costs a model call. The interface says next to each such button that it is one.

## The build

Input: a folder of texts (`.md`, `.txt`, `.docx`, `.odt`, `.html`), or texts uploaded to the organisation. One voice per register — seven blog posts and one legal notice give bands that fit neither. Fewer than four documents give min..max bands padded by 15 % instead of a percentile spread; the build says so.

Cost: measuring is free; the card is one model call of a few thousand tokens. The exemplars add a few hundred tokens to every prompt of every agent that carries the voice; that is the price of the feature and it is shown when a voice is assigned.

The build is deterministic apart from the card: the same corpus gives the same profile, exemplars and contrast list, so a rebuild after a metric change is safe and the card survives it.

## Correction pairs (slice 3, built)

The strongest signal for imitation is a pair: the agent's version of a text and the person's version of the same text. The next build shows the model the transformation directly, as before/after, which it learns better than from rules. A voice that collects pairs gets more exact with every correction, without anyone maintaining a corpus. The pairs stay in the organisation; they never leave it as training data.

**Where the first pairs come from: the approval gate.** The design above named the target systems — somebody edits a published post — and that is the second source, not the first. The first is the moment covey already has both halves on one screen: an action waiting for approval. A reviewer could only say yes or no there, so whoever disliked one sentence had to refuse and send the agent back to guess. They can now rewrite the text and approve THAT, which is both the better gate and the pair.

Three rules fall out of it, and each is a line in the code:

- **A rewrite belongs to an approval, never to a refusal.** What is denied does not go out, so there is nothing to correct.
- **An unchanged text is not a correction.** Otherwise every click would teach the voice that its own output is the standard.
- **The corrected text travels to the AGENT.** The agent performs the action, not the control plane ([`03`](03-lifecycle-scheduling.md)): the wake carries the reviewer's version and asks for it verbatim. Without that the approved text would be the one the reviewer had just replaced, and the edit would live only in the collection.

The agent's own half is found the way the style gate finds it (`style.ProseIn`) — one function for both, or one would eventually measure one text and store another.

**The second source: the agent brings the pair back.** The design assumed a plugin would notice the edit and hand the pair over. It cannot, and the reason is a property of the plugin contract rather than a gap in it: `target.System` is `Name`/`ActionSubject`/`Execute`/`PromptDoc`, `Execute` answers the AGENT, and neither the SDK nor the pack knows `COVEY_ACTION_PORT`. A plugin gets its own target system's credential and nothing else ([`04`](04-identity-secrets.md)), and that should stay so — it is not worth loosening to move a pair.

The agent is the only party on both sides: it can read what stands there now (every plugin has `get_ticket`, `get_page`, `get_note`, `get_message` or their like) and it can speak to the platform. So it files the pair itself, with `covey/correction` — one action beside `remember` and `style_check`, with its own guard-rail subject because a pair steers how every agent on that voice writes.

Three rules sit in that action, and each answers a way it could be wrong:

- **Who changed it is required, and a colleague is refused.** Only a person's edit is a correction; a second agent rewriting the text is a handover, and a voice that learns from it learns to imitate itself. covey checks the name against its own agents rather than trusting the label.
- **A typo is not a correction.** The corrected text has to be long enough to have a style (20 words), and enough words have to have moved: the larger of three words and five percent of the longer text. Word-based, so a reordered sentence counts as unchanged — what a voice learns from is different wording, not a different order.
- **It is the agent's word, and it is treated as such.** The same answer the wiki gives to the same weakness: every pair is in the recording, the subject can be gated, and a person can throw one out on the voice's page.

`POST /api/v1/voices/{id}/corrections` stays as the way in for anything outside a run — a script, an import, a person with the pair in front of them.

Storage is `voice_corrections` (migration 0091): the pair, the agent it came from, the action it was going into, and the source. The action is kept because register is not style — a correction on a mail says something different from one on a commit message. The pairs go into the card prompt of the next build, capped at the twenty newest, and stand on the voice's page where a person can throw out one that was made by accident.

## In covey (slice 2, built)

- `internal/voice` on top of `internal/style`: `Build(corpus, lang, reference) → Built` (profile, exemplars, contrast, notes), the card through `llm.Resolve`, and `Render(voice)` writing the `TONE.md`. The three measured artefacts are deterministic; only the card is a model call.
- Tables `voices` and `voice_documents` (migration 0090), plus `agents.voice_id` — what ACTS is the file in the agent's config, the column says whose voice it is. Since #471 the slots of `voice_assignments` are the assignment and act at dispatch; the column is read by nothing ([above](#voices-per-occasion-471)).
- The library page *Voices* beside Skills: the corpus, the build with its notes, the card to correct and release, the passages, the contrast, and the rendered `TONE.md` behind a fold — because the four artefacts on their own do not say what actually reaches a prompt.
- The picker in the agent settings (replaced by the slots per occasion in #471; the endpoint stays): assigning writes `TONE.md` as a new config version, so a change of voice is reviewable and revertible where every other change to an agent is ([`02`](02-agent-model.md)). Taking a voice off clears the link and LEAVES the file: removing it would change how an agent writes as a side effect of a picker.
- API: `/api/v1/voices` (+ `/documents`, `/build`, `/release`, and since #458 `/describe`, `/preview`, `/refine`) and `PUT /api/v1/agents/{id}/voice`; RBAC as for skills, the release included — that is the moment a description of somebody's hand starts appearing in every prompt of every agent carrying the voice.
- Ten locale catalogues for the UI text.

Two things turned out differently from the design above, and both for the same reason — not claiming what was not measured:

**The reference corpus is uploaded, not shipped.** The contrast list needs AI text to measure against, and a reference corpus nobody measured would be a number invented with a straight face. So a voice's corpus has two poles (`kind: author | reference`), the contrast appears once the second one exists, and until then the build says so in a note rather than showing an empty list. The organisation running writing agents has the honest reference: its own drafts.

**A build without a model still produces three quarters.** An installation without a control-plane credential gets profile, exemplars and contrast, and a note saying why there is no card. A feature that refuses everything because one of its four parts needs a model would make the other three unreachable.

## Slice 1 (done in covey-style)

`scripts/build_voice.py corpus/<folder> --name <name> --lang de|en [--card released.md]` writes `voices/<name>/{profile,exemplars,contrast,card,VOICE}.md`. The reference corpus is `reference/ai-de`, `reference/ai-en`. The `VOICE.md` is placed under `## Tone` in the writer agents' `SOUL.md` by hand; the yardstick is a post the owner writes himself, in the blog's register — the owner's tender documents give the hand, not the register.
