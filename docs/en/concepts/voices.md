---
slug: voices
title: Voices
description: 'How a covey agent writes like a person: a voice measured from texts, described in words, or drafted by an agent in the chat — a card, passages, a chat tone and, when measured, a profile the style gate checks against.'
faq:
  - q: We have no texts to upload. Can we still give an agent a voice?
    a: Yes. Describe in plain words how you write; one model call writes the card, five to eight sample passages and a suggested chat tone. Such a voice is described, not measured — the style gate does not check against it until texts are added and it is built.
  - q: Does anything a model wrote act without somebody reading it?
    a: No. The card and a described voice's passages are drafts until a person releases them on the voices page, and an agent can only be put on a voice that has something released or measured.
  - q: What does building a voice cost?
    a: Measuring texts is free. Writing the card, writing a voice from a description, a preview sample and a refinement are one bounded model call each, on the organisation's own credential; the page says so next to each button.
---

# Voices

A style profile says how *far* a text is from a corpus; it cannot make the text sound like the corpus's author. A **voice** can. It is an object of the organisation, assigned to agents in their settings, and assigning it writes the agent's `TONE.md` — a config version like any other.

A voice carries:

- a **card** — 250 to 400 words on how the author opens, carries an argument, brings in facts, builds sentences, and what they never do;
- **passages** — five to eight paragraphs that show the hand; they go into the prompt;
- a **chat tone** — address, tone and emoji in the team chat;
- when it is measured from texts: a **profile** the style gate checks every outgoing text against, and a **contrast** against AI text if a reference was uploaded.

## Building one

*Voices* in the library has a **New voice** button that walks through six steps; an existing voice shows the same steps as sections of its page. A rail on top marks each step as done, to do, optional or not yet possible, and the line under it names what blocks the next one.

1. **Purpose** — blog, support mail, chat, offers and proposals, or other. It goes into every prompt that writes in the voice and picks the kind of sample the preview writes.
2. **Source** — one of three:
   - **From texts.** Upload what somebody wrote (files or pasted text). While the texts go in, the page says what they lack: how many more texts of 150 words or more are needed for reliable bands (four in all), which texts are too short to carry a band, which address their reader differently from the rest ("these texts look like a different register"), which are in another language, and whose sentences run far longer or shorter than the others'. **Build** measures them; the card is one model call.
   - **From a description.** Say how you write, as you would to a new colleague — how a text begins, how facts come in, how long sentences are, what you never write, *du* or *Sie*. One model call writes the card, the passages and a suggested chat tone. The card's claims come from your description rather than from quoted passages, and the page marks the voice **described, not measured**.
   - **From the chat.** Ask an agent in the team chat to draft a voice. It interviews you in a few questions or takes texts you give it, and files a draft with `covey/voice_draft`. The draft appears on the voices page, named after the agent.
3. **Material** — the texts built, or the description written.
4. **Chat tone** — optional; a described voice brings a suggestion, which applies once saved.
5. **Preview** — optional; one short sample on a topic you pick, as a paragraph, a mail or a chat message. Not stored. Once a released card has a new draft beside it, either can be heard.
6. **Release** — read the card, correct it, or **refine it by instruction** ("less formal, more concrete numbers"): the revision is a draft shown beside the text before. Nothing of it acts until it is released; a released card is not overwritten by a later build.

## Described or measured, and the style gate

A voice built from a description has **no profile**. Nothing was measured, so the style gate does not check against it: it records that it skipped and lets the text pass. The voice still acts where it matters most — in the prompt, through its card and passages. Add texts to it and build, and it becomes measured: the passages come from the texts, the gate applies, and the released card stays.

## An agent drafting a voice

`covey/voice_draft {"name": "…", "purpose": "support_mail", "description": "…"}` — or `"texts": [{"name": "…", "text": "…"}]` instead of a description — creates the voice as a draft. It has its own guard-rail subject, `covey:voice_draft`, so an organisation can gate or forbid it, and every draft is in the recording. The agent cannot release, build or assign a voice: those stay with the people who manage voices.

## Who may do what

Every role reads voices. Creating, changing, building, describing, previewing, refining and releasing are for `org_admin` and `agent_owner` — the preview changes nothing, but it costs a model call.
