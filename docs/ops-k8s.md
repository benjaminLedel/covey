# Operations: reading a Kubernetes cluster

A practical runbook for the `k8s` plugin: a Kubernetes cluster as a target
system. It answers the questions an operator asks when something is wrong —
**why is this pod restarting, what did it say before it died, is that Ingress
pointing where the ticket claims, does this namespace have a NetworkPolicy at
all** — and it answers them by reading, not by changing.

> Short version: install the plugin from the catalogue, create a ServiceAccount
> bound to `view`, store `k8s_url` and `k8s_token`, store `k8s_ca` if the API
> server is self-signed, release the API server host in the egress allowlist,
> and unlock it in `ACCESS.md`.

---

## 1. Why it reads and does not write

The split is not caution for its own sake, it follows the deployment model. A
GitOps cluster reconciles its state from a repository (ArgoCD, Flux): a manifest
an agent applied directly would either be reverted on the next sync, or cause
drift somebody has to chase later. The write path for such a cluster already
exists and is the `gitlab` plugin — a merge request against the infrastructure
repository, reviewed like every other change.

Two write actions exist anyway, because they are operational rather than
declarative: `restart` bumps the annotation `kubectl rollout restart` bumps, and
`delete_pod` removes one stuck pod so its controller recreates it. Neither
changes desired state, so neither drifts from git.

**Secrets are never readable.** Not by RBAC accident but by construction: the
plugin has no path that returns one. To find out *which* secret a workload
expects, read the workload — the env and volume references name it without
revealing the value.

## 2. Installing it

`k8s` is a **catalogue plugin**, not a compiled one. Up to Covey 0.5.0 it
shipped inside the binary; from 0.6.0 it is a WebAssembly module installed from
the [plugin catalogue](https://github.com/benjaminLedel/covey-plugins), on the
same footing as anybody else's plugin.

Store → Catalogue → Kubernetes → Install. Covey verifies the digest the
catalogue pins before the module is stored, and refuses the install if the
artefact is not the one the entry names.

Upgrading across 0.6.0 with `k8s` already in use: the plugin row and its secrets
survive, only the code now arrives from the catalogue. Install it once
afterwards and the agents keep their access.

## 3. The actions at a glance

| Action | Scope | Parameters | Effect |
|---|---|---|---|
| `namespaces` | `read` | — | Every namespace with its phase. The cheapest way to see what the token can reach |
| `pods` | `read` | `namespace`, `selector` | The projection `kubectl get pods` shows: phase, readiness, restarts, image, node |
| `get` | `read` | `namespace`, `kind`, `name` | One object, decluttered |
| `list` | `read` | `namespace`, `kind` | Every object of a kind |
| `events` | `read` | `namespace`, `limit` | The cluster's own complaints, newest first |
| `logs` | `logs` | `namespace`, `name`, `container`, `tail_lines`, `previous` | Container logs |
| `restart` | `write` | `namespace`, `deployment` | Rollout restart |
| `delete_pod` | `write` | `namespace`, `name` | Delete one stuck pod |

`logs` has a scope of its own on purpose. A log contains whatever the
application printed, credentials included, and that is a different decision from
reading object state.

`previous: true` reads the log of the **crashed** container. That is where the
stack trace of a `CrashLoopBackOff` actually is — the running container was
started after it, and its log is usually empty.

## 4. Setup

**1. A ServiceAccount per agent role**, bound to the right ClusterRole. This is
the real limit, not the scope list:

- a review or security agent: the built-in `view` role, which excludes Secrets
- an ops agent: `view`, plus a small custom Role if it is to restart workloads
  (`patch` on deployments, `delete` on pods, in named namespaces only)

Put the ServiceAccount and its binding in the GitOps repository like every other
cluster object, so who may read what is reviewable in git history.

**2. Mint a token** — `kubectl create token <sa> --duration=…` for a short-lived
one, or a bound Secret for a long-lived one — and store it as the **agent-scoped**
secret `k8s_token`. Agent-scoped rather than org-scoped, so two agents can hold
two differently privileged tokens under the same name.

**3. Store the API server endpoint** as `k8s_url` (`https://…:6443`).

**4. If the API server is self-signed** — the k3s default — store the cluster CA
as `k8s_ca`. Covey builds the trust store from it and dials for the module.

> **Changed in 0.6.0.** The certificate used to be passed per action as
> `{{secret:k8s_ca}}` in a `ca_pem` parameter. It no longer is, and the
> parameter is gone. Passing a certificate through an action meant it travelled
> through the model's context, the guard-rail subject and the recording of every
> single call. Store it as `k8s_ca` and nothing else is needed.

There is deliberately no option to skip verification. A client that talks to a
production cluster without checking who answers is a man in the middle waiting
to happen, and the token it hands over is the one that reads every namespace.

**5. Release the API server host** in the agent's egress allowlist. The actions
run in the agent's sandbox and therefore go through the egress proxy; without
the entry every action fails with a note about the missing host.

**6. Unlock it in `ACCESS.md`:**

```
- system: k8s scope: read
```

Add `logs` for container logs. Add `write` only for an agent that should restart
workloads, and pair it with a guard rail on `k8s:delete_pod` if that should stay
a human decision.

## 5. Security model

- **RBAC is the authoritative limit, not the scopes.** A ServiceAccount bound to
  `view` cannot delete a pod however the agent's `ACCESS.md` reads. Covey's
  scopes shape what an agent is *told* it can do; Kubernetes decides what it can
  actually do. Both matter, in that order.
- **The module never sees the token.** It names a path; the host adds the
  endpoint and the credential. A WebAssembly module has no socket, so there is
  nothing to leak a token through even if it had one.
- **A redirect off the API server's origin is refused.** An endpoint able to
  answer "look over there" would otherwise aim the plugin at a host nobody
  allowed.
- **Every action is recorded** with its parameters and its result, under its own
  guard-rail subject (`k8s:logs`, `k8s:delete_pod`, …), so a policy can treat a
  deletion differently from a read.

## 6. Typical failure patterns

| Symptom | Cause |
|---|---|
| `kubernetes API: 403: … cannot list resource "pods" …` | RBAC. The message names the missing verb exactly — bind it or narrow the ask |
| `api server unreachable` | The API server host is missing from the egress allowlist |
| `x509: certificate signed by unknown authority` | Self-signed API server without `k8s_ca` stored |
| `restart` reports success and nothing restarts | Was a symptom of a frozen clock in the module and is fixed; if it recurs, the annotation is not changing — check the deployment's pod template |
| Empty log for a crashing pod | Read the crashed container: `"previous": true` |
