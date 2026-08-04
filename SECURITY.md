# Security policy

Covey brokers credentials, runs untrusted model output inside sandboxes and
enforces guard rails on an organisation's behalf. A hole in any of those is
worth reporting carefully.

## Reporting a vulnerability

**Please do not open a public issue.**

Use GitHub's private vulnerability reporting on this repository
(*Security* → *Report a vulnerability*). If that is not available to you, write
to <benjamin@schule-plus.com> with `covey security` in the subject.

Useful in a report: what you did, what happened, what you expected, and — if you
have it — the commit or release you tested. A proof of concept is welcome but
not required to file.

You will get an acknowledgement within a few days. This is a small project
without a paid security team, so please assume good faith over speed. We will
tell you when a fix lands and credit you in the release notes unless you would
rather stay anonymous.

## Supported versions

Covey has no long-term support branches yet. Fixes go to `main` and into the
next release; there is no backporting to older tags.

## Scope

In scope — anything that breaks one of the guarantees the platform claims:

- Escaping a sandbox, or reaching another agent's home directory or data.
- Obtaining a secret from the broker that the agent is not scoped for, or
  extracting a long-lived credential from inside a sandbox.
- Bypassing a guard rail, an approval gate, the egress control or the kill
  switch — including making the control plane fail *open*.
- Crossing an organisation boundary in a multi-tenant deployment, or an RBAC
  check that can be skipped.
- Authentication, session or token handling in the control plane and the
  daemon protocol.

Out of scope:

- Findings that require the attacker to already be a platform admin.
- Anything about the default bootstrap credentials
  (`admin@covey.local` / `covey-admin`). They are documented, deliberately
  well-known and meant to be changed — the README and the startup log both say
  so.
- An agent doing something unwise that its own configuration permitted. That is
  a configuration bug; guard rails are the mechanism, and `covey config lint`
  will often name it.
- Denial of service through sheer volume, and findings from automated scanners
  without a demonstrated impact.

## Operating Covey safely

If you are deploying rather than testing, the checklist in
[`docs/quickstart-docker.md`](docs/quickstart-docker.md) covers the things that
actually matter: change the admin password, keep `COVEY_MASTER_KEY` out of the
image and backed up, put TLS in front, and decide on egress isolation before
you give an agent real credentials.
