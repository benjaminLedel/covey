# Chat evaluation set

Scenarios for the two cheap turns an agent takes in a conversation (#457): the
**triage** (`chat.Triagieren` — answer, note, task or search) and the
**narration** (`chat.Erzaehlen` — telling the conversation what came out of a
task). Plus **addressing** scenarios: whether a group message speaks to the
agent at all (`chat.Addressed`), where the right answer is silence.

## Running it

```sh
go test ./internal/chat/       # hard checks against the recorded answers
make eval-chat                 # the same set against the real model
```

`go test` checks the recorded answer of every scenario and the deliberately
bad answers in `bad.json`, each of which must fail the checks it names — a
check that nothing can fail is not a check.

`make eval-chat` (`COVEY_EVAL_LLM=1`) runs every scenario through the real
turns, applies the same checks, and has a judge model on the fast tier grade
"sounds like a colleague in a work chat" from 1 to 5 with a one-line reason.
The report is printed and written to `last-report.md` here (ignored by git;
`COVEY_EVAL_REPORT` sets another path). The credential, in this order:
`ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, or `COVEY_EVAL_ORG` (an
organisation id) with `COVEY_MASTER_KEY` and `COVEY_DATABASE_URL`, which goes
through `llm.Resolve` like the control plane. Without one the test skips. A
run is roughly 60 short calls on the fast tier.

The judge score fails nothing; it is the number to compare two prompt
versions by. The hard checks do fail the live run, because a model's answer
that talks about "the task" is exactly what the set exists to catch.

## A scenario

One JSON file per scenario in `scenarios/`, named after its `name`:

| field | meaning |
|---|---|
| `name`, `language` (`de`/`en`), `kind` (`triage`/`narration`/`addressing`), `about` | what it is and why it is here |
| `conversation` | `kind` (`direct`/`group`), `title`, `members` besides the agent (`name`, `kind` `human`/`agent`, `slug`) |
| `person` | who writes: `name`, `job_title`, `department`, `responsibilities`; `technical` documents the case, the turn reads the job title |
| `agent` | `name`, `slug`, `role`, `soul` (an excerpt of SOUL.md) |
| `tone` | the chat tone (`address`, `tone`, `emoji`, `note`) as on a voice |
| `org_chart`, `open_tasks`, `finished_tasks`, `history`, `search_hits` | what the turn sees |
| `message` | the new message (triage, addressing) |
| `task` | `title`, `body`, `state` (`done`/`failed`), `result` (narration) |
| `expect` | `action` (the acceptable actions) or `addressed` |
| `checks` | `first_name`, `max_sentences`, `max_chars`, `must_contain` (all), `must_contain_any`, `must_not_contain` |
| `recorded` | a good answer as the model gives it: the raw triage output, or the chat line |

Always checked, beyond `checks`: the language; the length (defaults: three
sentences in a group, four in a direct conversation, one more for a
narration; a salutation like "Hi Ada!" counts as a sentence, so a greeting
scenario allows three: the salutation and two); no words of the machinery or of a service desk in German or
English ("Begrüßung beantwortet", "I have answered", "the task", "der Lauf",
"As an AI" …); no headings, bullets or bold; emoji as the tone allows; in
German, `du` or `Sie` as the tone says (`auto` follows the message).
