# Operations: giving Covey agents a browser

A practical runbook for the `browser` plugin: a **full headless Chrome** as a
universal adapter for web applications that have no target-system plugin of
their own. The agent navigates, reads page content, takes screenshots, clicks
and types — like a user, only without a visible window. A real Chromium is
driven over the DevTools protocol (chromedp, pure Go); "headless" means the
same engine, full rendering.

> Short version: enable the plugin, unlock it in `ACCESS.md`, add the target
> hosts to the **egress allowlist**. No secrets needed — the browser runs
> locally in the sandbox.

---

## 1. When the browser, when a plugin?

- **A target-system plugin of its own** (Zammad, Nextcloud, GitLab …) for
  systems worth a first-class, finely guard-railed integration: stable
  actions, clean `system:action` guard rails, brokered credentials.
- **The browser** for the **long tail**: internal web tools, portals and
  dashboards for which a plugin is not worth it. Application-agnostic — what a
  human can operate in a browser, the agent can too, without an API or a plugin.

The price: per-action guard rails apply more coarsely in the browser (a click is
opaque). The **egress allowlist** is the hard, load-bearing limit here.

## 2. The actions at a glance

| Action | Parameters | Effect |
|---|---|---|
| `navigate` | `url` | Load a page, returns the title + final URL (http/https only) |
| `content` | `selector` (optional) | Deliver the visible text (the whole page or by CSS selector, up to 20k characters) |
| `screenshot` | `to` (optional), `full` (optional) | Write a PNG into the sandbox (default `browser/shot-N.png`); `full=true` = the whole scrollable page |
| `click` | `selector` | Click an element |
| `type` | `selector`, `text` | Type text into a field |

The browser session **persists across several actions** (cookies/login are
preserved) and is ended when the sandbox falls asleep. The agent then reads
screenshots as images — that is how it "sees" the page.

## 3. Setup

1. Enable the plugin in the org (no secrets).
2. In the agent's `ACCESS.md`:
   ```
   - system: browser scope: navigate,content,screenshot,click,type
   ```
3. **Egress:** add every host the agent should call to the org's egress
   allowlist. Without approval no page loads — a failed navigation is almost
   always a missing egress rule.
4. **Sandbox image:** must contain `chromium` (already added in the bundled
   `Dockerfile.sandbox`). `COVEY_BROWSER_CHROME_PATH` overrides the browser
   path if necessary.

## 4. Security model

- **No long-lived secret in the sandbox:** the browser gets no brokered
  credentials automatically. If the agent has to log into a web app, the
  credentials come short-lived and scoped through the broker (e.g. as a
  deposited login the agent types into a form) — never permanently in the
  browser profile.
- **Egress is the hard limit:** the browser is the most powerful egress tool
  there is; the sandbox's network barrier gates every request.
- **Guard rails per action:** the subjects `browser:navigate`, `browser:click`,
  `browser:type`, `browser:content`, `browser:screenshot` — e.g. make
  `browser:type` subject to approval while `browser:navigate`/`content` stay
  free.
- **No local file access:** `navigate` only allows `http`/`https` —
  `file://` & co. are refused.
- **`--no-sandbox`:** Chromium's own setuid sandbox needs kernel capabilities
  the container does not have; since the container itself is the isolation
  boundary, Chromium runs with `--no-sandbox`. The hard limit remains the
  sandbox, not the browser's own sandbox.

## 5. Limits & expansion

- **Anti-bot/CAPTCHA:** some sites block automation. DOM control over CDP hits
  its limits there; the later pixel level (a computer-use runtime, `spec/16`)
  fares better, because it looks like a real user.
- **A visible window / live view:** `COVEY_BROWSER_HEADFUL` starts Chromium in
  visible mode — which then needs an X server/Xvfb in the image. That is the
  bridge to the live-view/takeover expansion stage (noVNC).
- **Timeout:** `COVEY_BROWSER_TIMEOUT_SECS` caps a single action
  (default 45 s).

## 6. Typical failure patterns

| Symptom | Cause | Remedy |
|---|---|---|
| "chromium start … " | Chromium missing in the image / wrong path | install `chromium` or set `COVEY_BROWSER_CHROME_PATH` |
| Navigation hangs/times out | The target host is not in the egress allowlist | add an egress rule |
| `nur http/https erlaubt` | A `file://`/`javascript:` URL | use a real web URL |
| A click finds nothing | The selector is invisible/absent | check with `content`/`screenshot`, correct the selector |
