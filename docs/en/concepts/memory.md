---
slug: memory
title: The wiki memory
description: 'How a covey agent retains knowledge: linked Markdown pages with a pgvector index instead of flat snippets — readable, correctable, condensed across runs.'
faq:
  - q: Where does my agents' memory live?
    a: In your Postgres database and in the home of the respective sandbox. None of it leaves your installation — unless you deliberately configure an external embedding service. If you want to avoid that, run the embedding yourself via Ollama.
  - q: Can I make an agent forget something specific?
    a: Yes. The wiki pages are visible and deletable in the interface. Because the memory consists of readable pages and not of vectors alone, you can also correct individual sentences instead of throwing everything away.
  - q: Does the memory grow without limit?
    a: 'It gets condensed: a scheduled maintenance pass merges duplicates and repairs dead links. On top of that you can switch on a platform-wide cleanup heartbeat in which every agent tends its own wiki.'
---

# The wiki memory

An agent that forgets what it learned after every task repeats every mistake and every piece of research. For that, covey gives it neither a transcript nor a heap of snippets but a **wiki**: linked Markdown pages, one per thing, searchable through a `pgvector` index.

## Why a wiki and not a snippet store

The usual design files every insight as a chunk of text and later searches by similarity. That finds phrasings, not connections — and connections are exactly what separates an agent that recites facts from one that knows a subject: customer → ticket → solution, customer → responsible colleague, project → repository → open issue.

In the wiki every thing has its page, and pages point at each other with `[[wikilinks]]`. Those links are the graph. As a side effect the result is readable by humans, which is the second reason: a memory nobody can look into cannot be corrected when it is wrong.

## Three operations

- **File** — at the end of a task the agent files its insights onto the right pages instead of creating a new one.
- **Look up** — when picking up a task, vector search returns the matching pages plus the compact index of the whole wiki: it therefore also knows what it does *not* know.
- **Condense** — a scheduled pass merges duplicates and repairs dead links, so knowledge gets denser rather than merely larger.

## Hybrid storage

Pages live twice: as `.md` files in the sandbox home, where the agent works with them as files, and in the control plane as the authoritative copy with the index. Lose the sandbox and no knowledge is lost.

## Embedding: built-in or external

With no further configuration a built-in embedding runs that measures word overlap — enough to try things out, but it will not find its own page again once the agent phrases things differently. For real semantic search, point `COVEY_EMBEDDING_PROVIDER` at a service (Voyage, OpenAI) or run one yourself via Ollama; existing pages are re-embedded automatically at the next start.

## Reaching in by hand

The memory is visible and editable in the interface: read a page, contribute something an agent could never have learned, or delete precisely what is wrong. That is the practical part of the promise — an agent you can teach without touching its prompt.

## For humans too

The same apparatus carries human memory: the [Companion app](../companion/companion.md) uses the same pages and the same index, only with a person as the owner.

## Next

- [The agent model](agent-model.md) — where memory shows up in a run
- [Backlog & lifecycle](backlog-and-lifecycle.md) — when things are filed and looked up
