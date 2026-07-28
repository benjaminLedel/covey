package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chromedp/chromedp"

	"covey/internal/target"
)

// System bindet den headless Chrome an die target-Registry. Kein Webhook,
// keine Credentials — der Browser lebt lokal in der Sandbox.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:          "browser",
		Label:         "Browser (headless Chrome)",
		Description:   "Ein vollwertiger headless Chrome als universeller Adapter für Web-Anwendungen ohne eigenes Plugin: Seiten öffnen (navigate), sichtbaren Text/DOM auslesen (content), Screenshots in die Sandbox schreiben (screenshot), klicken (click) und tippen (type). Läuft lokal im Daemon (chromedp/DevTools-Protokoll), braucht keine Secrets. Welche Seiten erreichbar sind, gatet die Egress-Allowlist.",
		Kind:          "builtin",
		Category:      target.CategoryWeb,
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. Plugin hier aktivieren — Secrets sind nicht nötig, alles läuft lokal
   in der Sandbox (nichts auf der Control Plane).

2. In der ACCESS.md des Agenten freischalten:
   - system: browser scope: navigate,content,screenshot,click,type

3. Egress ist entscheidend: der Browser ist das mächtigste Egress-Werkzeug.
   Jeder Host, den der Agent aufrufen darf, muss in der Egress-Allowlist der
   Org stehen — sonst lädt keine Seite. Eng führen.

4. Guard-Rails: jede Aktion ist ein eigenes Subjekt (browser:navigate,
   browser:click, …) und lässt sich gezielt auf Approval-Pflicht setzen.

Hinweis: Der Browser bleibt über eine Wach-Phase bestehen (Cookies/Login
bleiben erhalten) und wird beim Einschlafen der Sandbox beendet. Das
Sandbox-Image muss chromium enthalten (COVEY_BROWSER_CHROME_PATH übersteuert
den Pfad).`,
	})
}

func (System) Name() string { return "browser" }

// Kein Webhook-Eingang — der Browser nimmt keine externen Ereignisse an.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("browser hat keinen webhook-eingang")
}

// ActionSubject: jede Aktion ihr eigenes Guard-Rail-Subjekt — navigate/click
// lassen sich so schärfer regeln als das reine Lesen.
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
			out["hint"] = fmt.Sprintf("Inhalt über %d Zeichen — mit selector eingrenzen oder screenshot nutzen.", contentMax)
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
		// Optionale visuelle Annotation: den highlight-Treffer mit rotem Rahmen
		// (und optionalem Label) markieren, bevor der Screenshot fällt.
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
		// Annotation-Overlay wieder entfernen — die Seite bleibt bedienbar.
		if strings.TrimSpace(in.Highlight) != "" {
			_ = super.do(chromedp.Evaluate(`(()=>{const o=document.getElementById('covey-annot');if(o)o.remove();})()`, nil))
		}
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(local, buf, 0o644); err != nil {
			return nil, err
		}
		// Screenshot zusätzlich ins Recording geben (out-of-band als Blob) —
		// nicht ins Ergebnis, das an die Runtime zurückgeht.
		target.EmitArtifact(ctx, target.Artifact{MIME: "image/png", Bytes: buf})
		return map[string]any{"path": local, "size": len(buf),
			"hint": "Screenshot liegt lokal — direkt lesen, um die Seite zu sehen."}, nil

	case "click":
		sel := strings.TrimSpace(in.Selector)
		if sel == "" {
			return nil, fmt.Errorf("selector fehlt")
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
			return nil, fmt.Errorf("selector fehlt")
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
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
	}
}

// hitAttr markiert den aufgelösten :has-text-Treffer im DOM, damit chromedp
// ihn danach als reinen CSS-Selektor greifen kann.
const hitAttr = "data-covey-hit"

// hasTextRe erkennt das Playwright-artige :has-text("…")/:has-text('…') — kein
// gültiges CSS, das querySelector kennt.
var hasTextRe = regexp.MustCompile(`:has-text\(\s*(?:"([^"]*)"|'([^']*)')\s*\)`)

// parseHasText zerlegt einen Selektor mit :has-text("…") in seinen CSS-Präfix
// und den gesuchten Text. Ohne :has-text ist ok=false. Ein leerer Präfix wird
// zu "*" (jedes Element).
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

// resolveSelector übersetzt Playwright-artige :has-text("…")-Selektoren, die
// reines CSS nicht kennt, in einen konkreten CSS-Selektor. Der CSS-Präfix vor
// :has-text bleibt erhalten (button.primary:has-text("Anmelden") → nur Buttons
// mit sichtbarem Text „Anmelden"), getroffen wird der *innerste* sichtbare
// Treffer — wie bei Playwright. Reine CSS-Selektoren gehen unverändert durch.
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
		return "", fmt.Errorf(":has-text auflösen: %w", err)
	}
	if count == 0 {
		return "", fmt.Errorf("kein Element mit Text %q gefunden", needle)
	}
	return fmt.Sprintf("[%s=\"1\"]", hitAttr), nil
}

// annotate zeichnet vor einem Screenshot einen roten Rahmen (und optional ein
// Label) um den highlight-Treffer — so belegt der Agent visuell, WO ein Mangel
// sitzt. highlight ist ein CSS-Selektor oder :has-text("…"). Das Overlay liegt
// als #covey-annot über der Seite und wird nach dem Screenshot wieder entfernt.
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
		return fmt.Errorf("element nicht gefunden")
	}
	return nil
}

// cleanURL erzwingt ein http/https-Schema — file:// & Co. würden den Browser
// zum lokalen Dateizugriff missbrauchen und werden abgewiesen.
func cleanURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("url fehlt")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("url %q ist ungültig", raw)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("nur http/https erlaubt (nicht %q)", u.Scheme)
	}
	return u.String(), nil
}

// localPath löst einen Sandbox-Pfad sicher gegen das Arbeitsverzeichnis auf —
// kein Ausbruch per ".." oder absolutem Pfad außerhalb.
func localPath(ctx context.Context, p string) (string, error) {
	workdir := target.Workdir(ctx)
	if workdir == "" {
		return "", fmt.Errorf("keine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("lokaler pfad fehlt")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("pfad %q liegt ausserhalb des Sandbox-Arbeitsverzeichnisses", p)
	}
	return resolved, nil
}

func (System) PromptDoc() string {
	return `Verfügbare Browser-Aktionen (ein headless Chrome, den du wie ein Nutzer bedienst; die Sitzung bleibt
   über mehrere Aktionen erhalten — Cookies/Login bleiben):
   navigate {"url":"https://…"} öffnet eine Seite und liefert Titel + finale URL,
   content {"selector":"CSS-Selektor (optional, leer = ganze Seite)"} liefert den sichtbaren Text (bis 20k Zeichen),
   screenshot {"to":"lokaler/pfad (optional)","full":true,"highlight":"CSS-Selektor|:has-text(…) (optional)","label":"Text (optional)"}
   schreibt ein PNG in deine Sandbox (Default: browser/shot-N.png) und liefert den Pfad — danach die Datei lesen,
   um die Seite zu sehen; full=true nimmt die ganze scrollbare Seite statt nur den sichtbaren Bereich; highlight
   umrahmt das getroffene Element rot (mit optionalem label als Beschriftung) — so markierst du visuell, WO ein
   Mangel sitzt,
   click {"selector":"CSS-Selektor"} klickt das Element,
   type {"selector":"CSS-Selektor","text":"…"} tippt Text in ein Feld.
   Selektoren: reines CSS plus die Erweiterung :has-text("…") — trifft den innersten sichtbaren
   Treffer, dessen Text den String enthält (z. B. button:has-text("Anmelden"), a:has-text("Weiter")).
   Nützlich, wenn ein Button keine stabile id/class hat. Funktioniert in click, type und content.
   Arbeitsweise: erst navigate, dann mit content ODER screenshot orientieren, dann gezielt click/type. Für
   visuelle Seiten (Dashboards, Canvas) screenshot + lesen; für Text-Extraktion content mit passendem
   Selektor. WARTEN: der Browser hat keinen Webhook — nutze KEINEN blocked-Status; beende deinen Lauf mit done.
   Erreichbarkeit: Seiten laden nur, wenn ihr Host in der Egress-Allowlist steht — schlägt eine Navigation
   fehl, ist das oft die Ursache.`
}
