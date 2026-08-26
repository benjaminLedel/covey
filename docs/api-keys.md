# Operations: driving Covey from outside (API keys)

Everything the interface does, it does through `/api/v1/…`. This runbook is
about the second way of getting in there — the one a browser cannot hand out.

> Short version: *Account → API keys*, give it a name, copy the token once.
> Then `Authorization: Bearer covey_…` on every call. The key carries the
> rights of the seat it was minted for, and it can neither mint another key nor
> change a password.

## 1. Why there are two badges

The browser session is a cookie: `HttpOnly`, `SameSite=Strict`, renewed while
somebody works. Every one of those properties is right for a browser and
useless outside one — no script can read the cookie, and that is the point of
`HttpOnly`.

So anything that is not a browser had no route in at all: a pipeline that wants
to import an agent config, a script that reads costs at the end of the month,
the `covey-agent` skill creating an agent through the API. The consequence was
not that these things did not happen; it was that they happened **beside** the
product — in somebody's CI, with a copied cookie, or by hand. Operational
tooling belongs in the binary, because whoever installs Covey from the
repository has to have the same means as whoever runs the instance it came from.

## 2. Creating a key

*Account → API keys* (bottom of your own profile page):

1. **Name** — what is it for. This is what you will read when you decide months
   later whether it can go. A key called "test" is a key nobody dares revoke.
2. **Expires in (days)** — optional, empty means never. Give a key that belongs
   to one migration a fortnight; the alternative is that it lives forever
   because nobody remembers it exists.
3. **Create key.** The token appears **once**. Only its hash is stored, so
   there is no way to show it again — losing it means creating a new one, which
   is the correct amount of inconvenience for a credential.

What stays in the list is the prefix (`covey_a1b2c3d4…`), enough to tell two
keys apart and nothing you could use.

## 3. Using it

```bash
export COVEY_URL=https://covey.example
export COVEY_TOKEN=covey_…

curl -sS "$COVEY_URL/api/v1/agents" -H "Authorization: Bearer $COVEY_TOKEN"
```

The header is the only difference; every route the interface uses works the
same way. Importing an agent bundle, for instance:

```bash
curl -sS -X POST "$COVEY_URL/api/v1/agents/import" \
  -H "Authorization: Bearer $COVEY_TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @agent.bundle.json
```

## 4. What a key may do — and what it may not

**A key carries the rights of its seat.** Not more, not less. An auditor's key
reads; an org admin's key can do what an org admin can do. There is deliberately
no scope of its own: a scope that exists only on paper is worse than none,
because it reads like a restriction and enforces nothing. The role is the
boundary, and it is one that is already enforced on every route.

**A key hangs off a seat, not merely off an account.** An account can be a
member of several organisations, in different roles. A credential that did not
say which seat it works from would have an authority nobody can name — so the
key is bound to the seat you were working from when you created it, and it dies
with that seat.

**Two moves are reserved for the browser session:**

| Move | With a session | With a key |
|---|---|---|
| Read, write, everything the role permits | yes | yes |
| Change the display name | yes | yes |
| **Create or revoke an API key** | yes | **403** |
| **Change the password** | yes | **403** |

The reason is one sentence long: a credential that goes astray must not be able
to entrench itself. Minting a second key and locking the owner out are exactly
the two moves an attacker makes first, and both of them need the password.

## 5. Revoking

*Account → API keys → Revoke.* It takes effect on the next request — there is
no cache in front of it. The row is removed; who did what stays in the audit
log, which records the person, not the credential.

Revoke a key when you no longer know what uses it. The list shows **last used**
for exactly that decision: a key that has not been used in three months is
either dead or a spare set of house keys under the mat.

## 6. Typical failure patterns

| Symptom | Cause | Remedy |
|---|---|---|
| `401 not signed in` | The header is missing or misspelled. Only `Authorization: Bearer covey_…` counts — the prefix is part of the check. | Send the token unabridged, including `covey_`. |
| `401 api key invalid or expired` | Revoked, expired, or the seat is gone (removed from the organisation). | Create a new key. The answer is deliberately the same for all three: whoever probes must not be able to tell them apart. |
| `403 an API key cannot do this` | You are trying to mint/revoke a key or change a password with a key. | Do it in the browser — see section 4. |
| `403 role … has no rights here` | The seat's role does not cover this route. | Not a key problem: the same call fails in the browser. |
| `409 no_organization` | The account has no seat. | Join an organisation; a key without a seat cannot exist. |

## 7. Where the key must not end up

- Not in a repository, not in a `docker-compose.yml`, not in a CI variable that
  is readable by everyone with access to the pipeline.
- Not in an agent's `ACCESS.md` or bundle. An agent talks to Covey through the
  action proxy (`covey/…`), which needs no credential at all — a key inside a
  sandbox is a long-lived secret in the one place the whole architecture keeps
  them out of (`spec/04-identity-secrets.md`).
- Not in a URL. A header is not written into access logs; a query string is.
