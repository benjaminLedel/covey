---
slug: nextcloud
title: Nextcloud
description: A Nextcloud file store the agent lists, reads, edits and deposits files in. WebDAV, no OAuth flow — a share link is enough.
---

A practical runbook for the `nextcloud` plugin: a Nextcloud file store in which
the agent lists, reads, edits and deposits files. The technical basis is
**WebDAV** — unlike the SharePoint plugin there is no OAuth flow. The simplest
case is exactly what the title promises: **send a bot a share link**, the
plugin does the rest.

> Short version: share a folder in Nextcloud (link, "allow editing",
> password), set two secrets, unlock it in ACCESS.md. Nextcloud has
> **no webhook** here — if the agent needs an inbox folder as its working set,
> the intake runs by polling through `HEARTBEAT.md`.

---

## 1. Overview of the data flow

```
Nextcloud      ◄──(WebDAV, HTTPS)──────────  agent (sandbox, Claude Code)
(file store)       list, read, write,          │ actions through the action proxy,
                   download, upload,           │ credentials brokered per call
                   mkdir, delete               │ (nextcloud_url + nextcloud_token)
```

Two operating modes, recognised solely from `nextcloud_url`:

- **A share link** (`https://host/s/<token>`): WebDAV over
  `/public.php/webdav/`, basic auth with the share token as the user and the
  share password as the password. The shared folder is the root.
- **Account access** (the server base URL): WebDAV over
  `/remote.php/dav/files/<user>/`, basic auth with the user and an app
  password. The user's entire file directory is the root.

All the actions' paths are **relative to this root**; an escape via `..` is
refused daemon-side — the agent sees only what the link or the account releases.

---

## 2. Step-by-step instructions

### A) A public share link (recommended)

1. In Nextcloud open the folder the agent should work on →
   **Share → "Share link"**. Set the permission **"Allow editing"**
   (otherwise the agent may only read). Strongly recommended: put a
   **password** on the share — otherwise the link alone is the entire access.
2. Copy the share link, of the form `https://cloud.example.com/s/AbCdEf`.
3. Deposit under **Secrets** and assign to the agent:
   - `nextcloud_url` = the share link from step 2
   - `nextcloud_token` = the share password — **or `-`** if the share has no
     password (the broker demands a value; `-`, `none`, `anonymous`, `public`,
     `x` count as "no password").

### B) Account access (the whole file directory)

1. In Nextcloud → **Settings → Security → "Create app password"**.
   Never deposit the login password in Covey.
2. Deposit under **Secrets** and assign to the agent:
   - `nextcloud_url` = the server base URL, e.g. `https://cloud.example.com`
     (a subdirectory such as `/nextcloud` is preserved)
   - `nextcloud_token` = `user:app-password`

### For both routes

3. Unlock it in the agent's **ACCESS.md**:
   ```
   - system: nextcloud scope: read,write
   ```
4. **Egress:** deposit the Nextcloud host (e.g. `cloud.example.com`) as an
   egress host of the org — otherwise the sandbox cannot reach it.
5. Optional **intake by heartbeat** — in the agent's `HEARTBEAT.md`:
   ```
   - alle: 30m titel: Ablage sichten aufgabe: Liste mit list den
     Eingangsordner und bearbeite neue Dateien nach Playbook.
   ```

---

## 3. The actions at a glance

| Action | Parameters | Effect |
|---|---|---|
| `list` | `path` (optional) | List files/folders (max. 500 per call) |
| `read` | `path` | Deliver a text file directly (up to 1 MB, UTF-8 only) |
| `write` | `path`, `content` | Create/overwrite a text file (missing folders are created) |
| `download` | `path`, `to` (optional) | Fetch a file into the sandbox (default `nextcloud/<path>`) |
| `upload` | `from`, `to` (optional) | Deposit a file from the sandbox (replaces what is there) |
| `mkdir` | `path` | Create a folder path (`mkdir -p`) |
| `delete` | `path` | Delete a file/folder (the root is off limits) |

Binary and Office files (docx, xlsx, pdf, …) always go through
`download` → edit locally → `upload`; `read`/`write` are meant for plain
text files. Uploads are limited to 250 MB
(`COVEY_NEXTCLOUD_UPLOAD_MAX_MB` in the daemon env overrides the limit).
`write`/`upload` into a folder that does not yet exist create the missing
intermediate folders automatically (Nextcloud does not do that itself on PUT).

---

## 4. Security model

- **No long-lived secret in the sandbox:** the share token/app password live
  only in the daemon's RAM and are brokered per action. The runtime (the LLM
  process) never sees a secret.
- **Guard rails per action:** every subject is called `nextcloud:<action>` —
  `nextcloud:delete` or `nextcloud:write` can therefore be put behind an
  approval requirement specifically, while `nextcloud:list`/`read` stay free.
- **Path hardening:** remote paths are normalised (`..` refused), local paths
  resolved against the sandbox working directory — no escape into `/home` or
  the host file system.
- **Least privilege:** the share link limits the agent to exactly one folder by
  itself; that is the preferred route over account access, which opens the
  whole file directory. Set a share password.

---

## 5. Typical failure patterns

| Symptom | Cause | Remedy |
|---|---|---|
| `HTTP 401` on every action | The share password or `user:app-password` is wrong | check `nextcloud_token`; for a password-less share set `-` |
| `HTTP 403`/`404` when writing | The share is "view only" (no editing) | switch the share to "allow editing" |
| `HTTP 404 — path not found` | The path does not exist (relative to the root!) | check the actual tree with `list` |
| An action hangs/times out | The Nextcloud host is not released in the egress | add the org's egress host |
| Account access finds no files | The wrong user in `user:app-password` | check the user name — the file root is `/remote.php/dav/files/<user>/` |
