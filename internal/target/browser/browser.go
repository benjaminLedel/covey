// Package browser bindet einen vollwertigen headless Chrome als Zielsystem-
// Plugin an: der Agent navigiert, liest Seiteninhalt, macht Screenshots,
// klickt und tippt — der universelle Adapter für Web-Anwendungen, die kein
// eigenes Zielsystem-Plugin haben. Getrieben wird ein echter Chromium über
// das DevTools-Protokoll (chromedp, reines Go). „Headless" heißt nur „ohne
// sichtbares Fenster" — dieselbe Engine, volles Rendering; das „Sehen"
// liefert der Screenshot.
//
// Alles läuft lokal im Daemon der Sandbox (Descriptor.NoCredentials wie beim
// dev-Plugin); der Browser braucht keine Secrets. Welche Seiten er erreicht,
// gatet weiterhin die Egress-Allowlist der Org — der Browser ist das
// mächtigste Egress-Werkzeug, deshalb greifen ACCESS.md, Aktivierung und
// Guard-Rails (Subjekt browser:<aktion>) wie bei jedem Zielsystem.
package browser

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/chromedp"

	"covey/internal/target"
)

// shotCounter vergibt fortlaufende Default-Screenshot-Namen je Prozess.
var shotCounter atomic.Int64

func nextShotName() string {
	return fmt.Sprintf("shot-%d.png", shotCounter.Add(1))
}

// contentMax begrenzt, wie viel Seitentext content direkt in die Session
// zurückgibt — größere Seiten gehören per Screenshot oder gezieltem Selektor.
const contentMax = 20000

// actionTimeout deckelt eine einzelne Browser-Aktion (Navigation, Klick, …).
// Überschreibbar via COVEY_BROWSER_TIMEOUT_SECS.
func actionTimeout() time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_BROWSER_TIMEOUT_SECS"))); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 45 * time.Second
}

// manager hält einen persistenten Chromium über die Wach-Phase des Agenten
// (Cookies/Tabs/Login bleiben zwischen Aktionen erhalten) und serialisiert
// alle Läufe — ein chromedp-Kontext ist nicht nebenläufig bespielbar, und
// Covey arbeitet ohnehin seriell pro Agent.
type manager struct {
	mu            sync.Mutex
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
}

var super = &manager{}

func init() { target.OnShutdown(super.shutdown) }

// ensureLocked startet den Browser bei Bedarf (Lock muss gehalten sein) und
// liefert den Browser-Kontext, gegen den Aktionen laufen.
func (m *manager) ensureLocked() (context.Context, error) {
	if m.browserCtx != nil && m.browserCtx.Err() == nil {
		return m.browserCtx, nil
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	// Der Container IST die Sandbox — Chromiums eigener Setuid-Sandbox braucht
	// Kernel-Caps, die hier fehlen; --no-sandbox ist daher vertretbar. Kleiner
	// /dev/shm in Containern → --disable-dev-shm-usage. Feste Fenstergröße für
	// reproduzierbare Screenshots.
	opts = append(opts,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1440, 900),
	)
	if os.Getenv("COVEY_BROWSER_HEADFUL") != "" {
		// Sichtbarer Modus (braucht einen X-Server/Xvfb) — für die spätere
		// Live-View/Takeover-Ausbaustufe. Default bleibt headless.
		opts = append(opts, chromedp.Flag("headless", false))
	}
	if p := strings.TrimSpace(os.Getenv("COVEY_BROWSER_CHROME_PATH")); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	// Leerer Run erzwingt den Browser-Start und bindet den initialen Tab an den
	// langlebigen browserCtx — NICHT an ein Timeout-Kind, sonst risse dessen
	// cancel() den Tab gleich wieder ab. Ein fehlender Chromium wird hier als
	// klarer Fehler sichtbar (statt erst bei der ersten Navigation).
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return nil, fmt.Errorf("chromium start (ist chromium im Sandbox-Image installiert?): %w", err)
	}
	m.allocCancel, m.browserCtx, m.browserCancel = allocCancel, browserCtx, browserCancel
	return m.browserCtx, nil
}

// do führt eine Reihe chromedp-Aktionen atomar und serialisiert aus.
func (m *manager) do(tasks ...chromedp.Action) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	bctx, err := m.ensureLocked()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(bctx, actionTimeout())
	defer cancel()
	return chromedp.Run(runCtx, tasks...)
}

// shutdown beendet den Browser beim Herunterfahren der Sandbox — sonst
// überlebte der Chromium-Prozess die Wach-Phase.
func (m *manager) shutdown() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.browserCancel != nil {
		m.browserCancel()
	}
	if m.allocCancel != nil {
		m.allocCancel()
	}
	m.browserCtx, m.browserCancel, m.allocCancel = nil, nil, nil
}
