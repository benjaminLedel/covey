---
slug: vulndb
title: VulnDB
description: 'The public vulnerability databases as a target system: does an installed package version have a known vulnerability, and which version fixes it? npm, Packagist and Pub.'
---

A practical runbook for the `vulndb` plugin: the public vulnerability databases
as a target system. It answers one question well — **does a version of a package
this project has installed have a known vulnerability, and which version fixes
it?** Covered ecosystems: **npm**, **Packagist** (Composer/PHP) and **Pub**
(Dart/Flutter).

> Short version: install the plugin from the catalogue, unlock it in
> `ACCESS.md`, import the egress template "Vulnerability databases". No secret
> needed, and as of 0.6.0 none is used.

---

## 1. Why a plugin and not curl in the sandbox

All four sources are publicly reachable, so an agent could fetch them with
`dev exec` and `curl`. Three things are missing then, and they are the reason
this plugin exists:

- **Guard rails do not apply.** For everything the agent shells, the subject is
  `dev:exec`. A policy cannot tell "may ask osv.dev" from "may run arbitrary
  shell commands". The egress proxy filters hosts, not intentions.
- **A key would be a long-lived secret in the sandbox** — which the design
  principles forbid. Through the plugin the key comes from the broker per call
  and never lands in the sandbox.
- **The agent would rebuild the calls in every run.** Lock file parsing, batch
  building, evaluating JSON without `jq` — that costs turns and is invented
  anew each time. Here it is written and tested once.

What the plugin deliberately does **not** replace: `npm audit` and
`composer audit` resolve the **dependency graph** and thereby answer who pulls a
vulnerable package in. That stays a job for `dev exec` in the checkout.

## 2. The actions at a glance

| Action | Parameters | Effect |
|---|---|---|
| `scan_lockfile` | `path` | Read a lock file and match every installed version against OSV in one batch |
| `query_batch` | `packages[]` | The same check for name/version pairs determined by hand |
| `query` | `ecosystem`, `name`, `version` | A single package |
| `advisory` | `id` | An advisory's details, merged from OSV, GHSA and NVD |
| `latest_version` | `ecosystem`, `name` | The registry's version list — does the fix version exist, what came after? |

`scan_lockfile` reads `package-lock.json`, `npm-shrinkwrap.json`,
`composer.lock` and `pubspec.lock`. `yarn.lock` and `pnpm-lock.yaml` are
**deliberately not parsed**: their format has changed several times across major
versions, and a half-read lock file reports fewer packages than there are —
which reads like an all-clear. The way out is in the error message: extract the
pairs yourself and send them through `query_batch`.

## 3. Setup

> **Installing it.** As of 0.6.0 this is a **catalogue plugin**, not a compiled
> one — Store → Catalogue → Vulnerability databases → Install. covey verifies the digest the
> catalogue pins before storing the module. Upgrading across 0.6.0 with the
> plugin already in use: the plugin row and its secrets survive, only the code
> now arrives from the catalogue, so install it once afterwards and the agents
> keep their access.

1. Enable the plugin in the org (no secrets).
2. In the agent's `ACCESS.md`:
   ```
   - system: vulndb scope: read
   ```
3. **Egress:** import the built-in template **"Vulnerability databases"** and
   assign it to the agent. The actions run in the sandbox daemon and therefore
   go through the egress proxy — without the hosts every action fails with a
   note about the missing host. The template contains:
   `api.osv.dev`, `osv.dev`, `api.github.com`, `services.nvd.nist.gov`,
   `registry.npmjs.org`, `packagist.org`, `repo.packagist.org`, `pub.dev`.
4. **There is nothing else to configure.** All four sources are public and the
   plugin holds no credential at all.

   > **Changed in 0.6.0.** `vulndb_token` no longer has an effect. It used to
   > raise the NVD limit from 5 requests per 30 seconds to 50; NVD now always
   > answers at the anonymous limit.
   >
   > The reason is structural rather than an oversight. `vulndb` became a
   > WebAssembly module, and a module is never handed the brokered credential
   > for a host it merely *declared* — the token belongs to the system an
   > organisation pointed the plugin at, and this plugin points at nobody. A
   > token that travels to a declared host is a token that leaked, so the rule
   > is worth more than the rate limit.
   >
   > What it costs: `advisory` may report `nvd: … HTTP 429` as a note beside an
   > otherwise complete result, the same as any source that does not answer. A
   > stored `vulndb_token` is now dead configuration and can be deleted.

A ready-made agent that uses all of this:
[`examples/dependency-security-agent.bundle.json`](../../../examples/README.md).

## 4. The two results that look alike and are not

This is the failure mode worth knowing:

- `"clean": true` — checked, nothing found. An all-clear.
- an empty findings list **with `notes`** — something could not be checked. A
  gap.

A project without a lock file, an ecosystem whose database did not answer, a
package from a git source: none of that is "no vulnerabilities", it is "no
statement". The plugin keeps the two apart in the result; the agent config has
to keep them apart in the report.

The plugin also needs a **lock file**. A manifest (`package.json`,
`composer.json`, `pubspec.yaml`) declares ranges, and a range is not a fact.
A project without a committed lock file is a finding of its own.

## 5. Fix branches — the detail everything hangs on

An advisory often has **several fix branches**: 2.4.5 for the 2.x line, 3.1.2
for 3.x. Whoever names the wrong one proposes a major upgrade where a patch
version would have done.

`scan_lockfile` therefore picks the branch the installed version sits on and
reports it as `fixed`. If the version cannot be compared — a Composer
`dev-master`, an unusual scheme — **no** fix version is claimed: `fixed` stays
empty and `fixed_candidates` holds all of them. Both empty means there is no fix
yet.

## 6. Security model

- **No secret at all.** Not "optional" any more: the module holds no credential
  and cannot be given one, because every source it reaches is a declared host
  rather than a brokered system. There is nothing here to leak.
- **Guard rails per action:** the subjects `vulndb:scan_lockfile`,
  `vulndb:query`, `vulndb:query_batch`, `vulndb:advisory`,
  `vulndb:latest_version`. All are read-only; the separation exists for
  traceability and so that the expensive full scan can be governed separately
  from the cheap single lookup.
- **Egress remains the hard limit.** The actions leave the sandbox like every
  other outward contact.
- **Only lock files are read**, and only under the sandbox working directory or
  at an absolute path the agent already has; nothing is written.

## 7. Typical failure patterns

| Symptom | Cause | Remedy |
|---|---|---|
| "… not reachable … egress allowlist" | The host is missing from the allowlist | import/assign the template "Vulnerability databases" |
| "rate limit reached (HTTP 429)" | NVD at the anonymous limit | query fewer CVEs per run and space the runs out — a key is no longer possible, see section 3 |
| "unknown lock file" | yarn/pnpm or an unsupported ecosystem | extract the pairs and use `query_batch` |
| Findings without `fixed`, but with `fixed_candidates` | The version could not be compared to the branches | name the candidates in the ticket instead of picking one |
| An empty result with `notes` | Something could not be checked | report it as a gap, never as an all-clear |
