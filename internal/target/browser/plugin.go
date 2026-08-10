package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chromedp/chromedp"

	"covey/internal/target"
)

// System binds the headless Chrome to the target registry. No webhook, no
// credentials — the browser lives locally in the sandbox.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:          "browser",
		Label:         "Browser (headless Chrome)",
		Description:   "A full headless Chrome as the universal adapter for web applications without a plugin of their own: open pages (navigate), read visible text/DOM (content), write screenshots into the sandbox (screenshot), click (click) and type (type). Runs locally in the daemon (chromedp/DevTools protocol), needs no secrets. Which pages are reachable is gated by the egress allowlist.",
		Kind:          "builtin",
		Category:      target.CategoryWeb,
		Scopes:        []string{"navigate", "content", "screenshot", "click", "type"},
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Activate the plugin here — no secrets are needed, everything runs locally
   in the sandbox (nothing on the control plane).

2. Enable it in the agent's ACCESS.md:
   - system: browser scope: navigate,content,screenshot,click,type

3. Egress is decisive: the browser is the most powerful egress tool. Every
   host the agent may call must be on the org's egress allowlist — otherwise
   no page loads. Keep it narrow.

4. Guard-rails: every action is its own subject (browser:navigate,
   browser:click, …) and can be put under approval individually.

Note: the browser persists across a waking phase (cookies/login are kept) and
is terminated when the sandbox goes to sleep. The sandbox image must contain
chromium (COVEY_BROWSER_CHROME_PATH overrides the path).`,
	})
}

func (System) Name() string { return "browser" }

// Kein target.Webhooker: der Browser nimmt keine externen Ereignisse an. Die
// Schnittstelle wird deshalb weggelassen und nicht mit ablehnenden Rueempfen
// erfuellt — wer sie implementiert, meldet damit eine Faehigkeit an, und die
// Einrichtung baut daraufhin einen Webhook-Schritt samt Adresse und
// Secret-Hinweis, den niemand befolgen kann.

// ActionSubject: every action gets its own guard-rail subject — navigate/click
// can thus be governed more strictly than plain reading.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "browser:" + action
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, _ target.Credential) (any, error) {
	var in struct {
		URL       string `json:"url"`
		Selector  string `json:"selector"`
		Text      string `json:"text"`
		To        string `json:"to"`
		Full      bool   `json:"full"`
		Highlight string `json:"highlight"`
		Label     string `json:"label"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("params: %w", err)
		}
	}

	switch action {
	case "navigate":
		u, err := cleanURL(in.URL)
		if err != nil {
			return nil, err
		}
		var title, loc string
		if err := super.do(
			chromedp.Navigate(u),
			chromedp.Title(&title),
			chromedp.Location(&loc),
		); err != nil {
			return nil, fmt.Errorf("navigate %q: %w", u, err)
		}
		return map[string]any{"url": loc, "title": title}, nil

	case "content":
		var text string
		var task chromedp.Action
		if sel := strings.TrimSpace(in.Selector); sel != "" {
			eff, err := resolveSelector(sel)
			if err != nil {
				return nil, fmt.Errorf("content: %w", err)
			}
			task = chromedp.Text(eff, &text, chromedp.ByQuery, chromedp.NodeVisible)
		} else {
			task = chromedp.Evaluate(`document.body ? document.body.innerText : ""`, &text)
		}
		if err := super.do(task); err != nil {
			return nil, fmt.Errorf("content: %w", err)
		}
		out := map[string]any{"text": text}
		if len(text) > contentMax {
			out["text"] = text[:contentMax]
			out["truncated"] = true
			out["hint"] = fmt.Sprintf("content over %d characters — narrow it down with selector or use screenshot.", contentMax)
		}
		return out, nil

	case "screenshot":
		dest := in.To
		if dest == "" {
			dest = filepath.Join("browser", nextShotName())
		}
		local, err := localPath(ctx, dest)
		if err != nil {
			return nil, err
		}
		// Optional visual annotation: mark the highlight hit with a red frame (and
		// an optional label) before the screenshot is taken.
		if hl := strings.TrimSpace(in.Highlight); hl != "" {
			if err := annotate(hl, in.Label); err != nil {
				return nil, fmt.Errorf("highlight %q: %w", hl, err)
			}
		}
		var buf []byte
		shot := chromedp.CaptureScreenshot(&buf)
		if in.Full {
			shot = chromedp.FullScreenshot(&buf, 90)
		}
		if err := super.do(shot); err != nil {
			return nil, fmt.Errorf("screenshot: %w", err)
		}
		// Remove the annotation overlay again — the page stays operable.
		if strings.TrimSpace(in.Highlight) != "" {
			_ = super.do(chromedp.Evaluate(`(()=>{const o=document.getElementById('covey-annot');if(o)o.remove();})()`, nil))
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(local, buf, 0o644); err != nil {
			return nil, err
		}
		// Additionally hand the screenshot to the recording (out-of-band as a
		// blob) — not into the result that goes back to the runtime.
		target.EmitArtifact(ctx, target.Artifact{MIME: "image/png", Bytes: buf})
		return map[string]any{"path": local, "size": len(buf),
			"hint": "the screenshot is local — read it directly to see the page."}, nil

	case "click":
		sel := strings.TrimSpace(in.Selector)
		if sel == "" {
			return nil, fmt.Errorf("selector missing")
		}
		eff, err := resolveSelector(sel)
		if err != nil {
			return nil, fmt.Errorf("click %q: %w", sel, err)
		}
		if err := super.do(chromedp.Click(eff, chromedp.ByQuery, chromedp.NodeVisible)); err != nil {
			return nil, fmt.Errorf("click %q: %w", sel, err)
		}
		return map[string]any{"clicked": sel}, nil

	case "type":
		sel := strings.TrimSpace(in.Selector)
		if sel == "" {
			return nil, fmt.Errorf("selector missing")
		}
		eff, err := resolveSelector(sel)
		if err != nil {
			return nil, fmt.Errorf("type in %q: %w", sel, err)
		}
		if err := super.do(chromedp.SendKeys(eff, in.Text, chromedp.ByQuery, chromedp.NodeVisible)); err != nil {
			return nil, fmt.Errorf("type in %q: %w", sel, err)
		}
		return map[string]any{"typed": sel}, nil

	default:
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
}

// hitAttr marks the resolved :has-text hit in the DOM so that chromedp can
// afterwards grab it as a plain CSS selector.
const hitAttr = "data-covey-hit"

// hasTextRe recognizes the Playwright-style :has-text("…")/:has-text('…') —
// not valid CSS that querySelector would know.
var hasTextRe = regexp.MustCompile(`:has-text\(\s*(?:"([^"]*)"|'([^']*)')\s*\)`)

// parseHasText splits a selector containing :has-text("…") into its CSS prefix
// and the text being looked for. Without :has-text, ok=false. An empty prefix
// becomes "*" (any element).
func parseHasText(sel string) (css, needle string, ok bool) {
	m := hasTextRe.FindStringSubmatch(sel)
	if m == nil {
		return "", "", false
	}
	needle = m[1]
	if needle == "" {
		needle = m[2]
	}
	css = strings.TrimSpace(hasTextRe.ReplaceAllString(sel, ""))
	if css == "" {
		css = "*"
	}
	return css, needle, true
}

// resolveSelector translates Playwright-style :has-text("…") selectors, which
// plain CSS does not know, into a concrete CSS selector. The CSS prefix before
// :has-text is preserved (button.primary:has-text("Sign in") → only buttons
// with the visible text "Sign in"); what is matched is the *innermost* visible
// hit — as in Playwright. Plain CSS selectors pass through unchanged.
func resolveSelector(sel string) (string, error) {
	css, needle, ok := parseHasText(sel)
	if !ok {
		return sel, nil
	}
	c, _ := json.Marshal(css)
	n, _ := json.Marshal(needle)
	js := fmt.Sprintf(`(() => {
  const hits = Array.from(document.querySelectorAll(%s))
    .filter(e => (e.innerText || e.textContent || '').includes(%s));
  if (!hits.length) return 0;
  const vis = hits.filter(e => e.offsetParent !== null || e.getClientRects().length);
  const pool = vis.length ? vis : hits;
  const inner = pool.filter(e => !pool.some(o => o !== e && e.contains(o)));
  const el = inner[0] || pool[0];
  document.querySelectorAll('[%s]').forEach(x => x.removeAttribute('%s'));
  el.setAttribute('%s', '1');
  return hits.length;
})()`, c, n, hitAttr, hitAttr, hitAttr)
	var count int
	if err := super.do(chromedp.Evaluate(js, &count)); err != nil {
		return "", fmt.Errorf("resolving :has-text: %w", err)
	}
	if count == 0 {
		return "", fmt.Errorf("no element with text %q found", needle)
	}
	return fmt.Sprintf("[%s=\"1\"]", hitAttr), nil
}

// annotate draws a red frame (and optionally a label) around the highlight hit
// before a screenshot — that is how the agent shows visually WHERE a defect
// sits. highlight is a CSS selector or :has-text("…"). The overlay sits over
// the page as #covey-annot and is removed again after the screenshot.
func annotate(highlight, label string) error {
	eff, err := resolveSelector(highlight)
	if err != nil {
		return err
	}
	s, _ := json.Marshal(eff)
	l, _ := json.Marshal(label)
	js := fmt.Sprintf(`(() => {
  const el = document.querySelector(%s);
  if (!el) return false;
  el.scrollIntoView({block:'center',inline:'center'});
  const r = el.getBoundingClientRect();
  const old = document.getElementById('covey-annot'); if (old) old.remove();
  const c = document.createElement('div');
  c.id = 'covey-annot';
  c.style.cssText = 'position:fixed;inset:0;z-index:2147483647;pointer-events:none';
  const box = document.createElement('div');
  box.style.cssText = 'position:absolute;border:3px solid #e5484d;border-radius:4px;box-shadow:0 0 0 3px rgba(229,72,77,.35);left:'+(r.left-3)+'px;top:'+(r.top-3)+'px;width:'+(r.width+6)+'px;height:'+(r.height+6)+'px';
  c.appendChild(box);
  const text = %s;
  if (text) {
    const lab = document.createElement('div');
    lab.textContent = text;
    let top = r.top - 26; if (top < 2) top = r.bottom + 6;
    lab.style.cssText = 'position:absolute;left:'+Math.max(2,r.left-3)+'px;top:'+top+'px;background:#e5484d;color:#fff;font:600 13px/1.4 system-ui,sans-serif;padding:2px 8px;border-radius:4px;white-space:nowrap;max-width:90vw;overflow:hidden;text-overflow:ellipsis';
    c.appendChild(lab);
  }
  document.body.appendChild(c);
  return true;
})()`, s, l)
	var ok bool
	if err := super.do(chromedp.Evaluate(js, &ok)); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("element not found")
	}
	return nil
}

// cleanURL enforces an http/https scheme — file:// and friends would abuse the
// browser for local file access and are rejected.
func cleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url missing")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("url %q is invalid", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("only http/https allowed (not %q)", u.Scheme)
	}
	return u.String(), nil
}

// localPath resolves a sandbox path safely against the working directory — no
// escape via ".." or an absolute path outside it.
func localPath(ctx context.Context, p string) (string, error) {
	workdir := target.Workdir(ctx)
	if workdir == "" {
		return "", fmt.Errorf("no sandbox (no working directory in the context)")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("local path missing")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q lies outside the sandbox working directory", p)
	}
	return resolved, nil
}

func (System) PromptDoc() string {
	return `Available browser actions (a headless Chrome you operate like a user; the session persists
   across several actions — cookies/login are kept):
   navigate {"url":"https://…"} opens a page and returns the title + final URL,
   content {"selector":"CSS selector (optional, empty = the whole page)"} returns the visible text (up to 20k characters),
   screenshot {"to":"local/path (optional)","full":true,"highlight":"CSS selector|:has-text(…) (optional)","label":"text (optional)"}
   writes a PNG into your sandbox (default: browser/shot-N.png) and returns the path — then read the file
   to see the page; full=true takes the whole scrollable page instead of just the visible area; highlight
   frames the matched element in red (with an optional label as a caption) — that is how you mark visually WHERE a
   defect sits,
   click {"selector":"CSS selector"} clicks the element,
   type {"selector":"CSS selector","text":"…"} types text into a field.
   Selectors: plain CSS plus the extension :has-text("…") — it matches the innermost visible
   hit whose text contains the string (e.g. button:has-text("Sign in"), a:has-text("Next")).
   Useful when a button has no stable id/class. Works in click, type and content.
   How to work: navigate first, then orient yourself with content OR screenshot, then click/type deliberately. For
   visual pages (dashboards, canvas) screenshot + read; for text extraction content with a fitting
   selector. WAITING: the browser has no webhook — do NOT use the blocked status; end your run with done.
   Reachability: pages only load when their host is on the egress allowlist — if a navigation
   fails, that is often the cause.`
}
