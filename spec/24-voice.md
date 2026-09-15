# 24 — Voice: an author's style as an object an agent carries

**Status: slices 1 to 3 are built.** Slice 1 lives in the covey-style skill, slices 2 and 3 in covey (#195, #257).

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

A **voice** is an organisation-level object, versioned, built once from texts a person uploads, assigned to an agent in its settings. Four artefacts:

1. **Profile** — the ```style-profile``` block as in [`02-agent-model.md`](02-agent-model.md): bands per metric, the lexicon, plus `antithesis_rate`. The guard. Address metrics (wir, ich, Sie, du, man, questions) leave the profile by default: a tender document says "Sie" and never "wir", a blog by the same author may say both; they belong to the register of the corpus, not to the author's hand.
2. **Exemplars** — five to eight paragraphs chosen from the corpus for variety: an opening, one that carries evidence, one with an example, a long one, a short one, a closing. They go into the compiled prompt under `## Tone`. A paragraph that closes on an antithesis is never an exemplar; the same paragraph reused in two documents counts once.
3. **Style card** — 250 to 400 words in the corpus's language: how the author opens, carries an argument, holds on to facts, builds sentences and paragraphs, and what they never do. Written once by the organisation's model from exemplars and measurements, every claim tied to a quoted passage of at most eight words; a claim without a passage is left out. A person releases the card; a released card is not rewritten by the next build.
4. **Contrast list** — the metrics on which a reference corpus of AI text sits a full band width outside the author's band, in words: "dashes per 1000 words: the author never (0.0); a model 10.3". "Never" is the sharpest signal a corpus yields, and it falls out of the comparison on its own. covey ships the reference corpus.

Knowledge and experience are not part of a voice. They come from the model and from covey's own material — the wiki, the recording, the specification; a text without material sounds like a model in any voice. The measured lever for that is the anchor metrics, not the card.

## Where each artefact acts

| Moment | What acts | Where |
|---|---|---|
| writing | exemplars and card in the prompt | `agents.CompilePrompt`, the `## Tone` section |
| revising | card as the editor rules, exemplars beside the findings | `covey/style_apply` ([`06`](06-observability-control.md)) |
| leaving | profile bands and the contrast list as findings with evidence | the style gate |

The gate keeps acting on HIGH findings only; the contrast list turns the author's "never" into bands whose upper edge is close to zero, so a single dash is a MEDIUM and a page of them a HIGH.

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
- Tables `voices` and `voice_documents` (migration 0090), plus `agents.voice_id` — what ACTS is the file in the agent's config, the column says whose voice it is.
- The library page *Voices* beside Skills: the corpus, the build with its notes, the card to correct and release, the passages, the contrast, and the rendered `TONE.md` behind a fold — because the four artefacts on their own do not say what actually reaches a prompt.
- The picker in the agent settings: assigning writes `TONE.md` as a new config version, so a change of voice is reviewable and revertible where every other change to an agent is ([`02`](02-agent-model.md)). Taking a voice off clears the link and LEAVES the file: removing it would change how an agent writes as a side effect of a picker.
- API: `/api/v1/voices` (+ `/documents`, `/build`, `/release`) and `PUT /api/v1/agents/{id}/voice`; RBAC as for skills, the release included — that is the moment a description of somebody's hand starts appearing in every prompt of every agent carrying the voice.
- Ten locale catalogues for the UI text.

Two things turned out differently from the design above, and both for the same reason — not claiming what was not measured:

**The reference corpus is uploaded, not shipped.** The contrast list needs AI text to measure against, and a reference corpus nobody measured would be a number invented with a straight face. So a voice's corpus has two poles (`kind: author | reference`), the contrast appears once the second one exists, and until then the build says so in a note rather than showing an empty list. The organisation running writing agents has the honest reference: its own drafts.

**A build without a model still produces three quarters.** An installation without a control-plane credential gets profile, exemplars and contrast, and a note saying why there is no card. A feature that refuses everything because one of its four parts needs a model would make the other three unreachable.

## Slice 1 (done in covey-style)

`scripts/build_voice.py corpus/<folder> --name <name> --lang de|en [--card released.md]` writes `voices/<name>/{profile,exemplars,contrast,card,VOICE}.md`. The reference corpus is `reference/ai-de`, `reference/ai-en`. The `VOICE.md` is placed under `## Tone` in the writer agents' `SOUL.md` by hand; the yardstick is a post the owner writes himself, in the blog's register — the owner's tender documents give the hand, not the register.
