<!--
Thanks for the pull request. Keep it to one coherent change — a branch that
does three things is three times as hard to review and to revert.
-->

## What this changes

<!-- What it does and, more importantly, why. -->

## Where it belongs

<!--
If this changes how the system behaves, name the spec document that covers it
(e.g. spec/03-lifecycle-scheduling.md) — or say why none does yet. Pure
implementation changes can just say "implementation only".
-->

Closes #

## Checklist

- [ ] `make test` passes.
- [ ] `make test-integration` passes, or does not apply. (It skips silently when the dev database on port 5433 is not up — check that it really ran.)
- [ ] New user-facing strings are in **both** `web/src/locales/de.json` and `en.json`.
- [ ] Database changes are a **new** migration pair; no existing migration was edited.
- [ ] `README.md` and `README.de.md` were changed together, or neither.
- [ ] Behaviour changes are reflected in `spec/`, docs changes in `docs/` — both in English.
- [ ] No long-lived secret ends up inside a sandbox; guard rails still fail closed.
