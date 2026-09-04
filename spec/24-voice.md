# 24 — Voice: an author's style as an object an agent carries

**Status: design (issue #195). Slice 1 exists in the covey-style skill; slices 2 and 3 are open.**

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

## Correction pairs (slice 3)

The strongest signal for imitation is a pair: the agent's version of a text and the person's version of the same text. When a person edits an agent's text in a target system — a CMS post, a wiki page — covey stores the pair to the voice the agent carried. The next build shows the model the transformation directly, as before/after, which it learns better than from rules. A voice that collects pairs gets more exact with every correction, without anyone maintaining a corpus. The pairs stay in the organisation; they never leave it as training data.

## In covey (slice 2)

- `internal/voice` on top of `internal/style`: the build (`Build(corpus) → Voice`), exemplar selection, the contrast against the shipped reference, the card prompt through `llm.Resolve`.
- Tables `voices` (organisation, name, language, version, released card) and `voice_documents` (the uploaded texts, per voice); a migration of their own.
- The library page `Voices` beside Skills: upload, build with the cost shown, the card for release, versions.
- The tone picker in the agent settings (noted on #183): choosing a voice writes `TONE.md` from `VOICE.md`; the file is versioned with the rest of the config, so a change of voice is a config version like any other ([`02`](02-agent-model.md)).
- API: `/api/v1/voices`, `/api/v1/voices/{id}/build`, `/api/v1/voices/{id}/release`; RBAC as for skills.
- Ten locale catalogues for the UI text.

## Slice 1 (done in covey-style)

`scripts/build_voice.py corpus/<folder> --name <name> --lang de|en [--card released.md]` writes `voices/<name>/{profile,exemplars,contrast,card,VOICE}.md`. The reference corpus is `reference/ai-de`, `reference/ai-en`. The `VOICE.md` is placed under `## Tone` in the writer agents' `SOUL.md` by hand; the yardstick is a post the owner writes himself, in the blog's register — the owner's tender documents give the hand, not the register.
