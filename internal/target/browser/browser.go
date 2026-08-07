// Package browser binds a full headless Chrome as a target-system plugin: the
// agent navigates, reads page content, takes screenshots, clicks and types —
// the universal adapter for web applications that have no target-system
// plugin of their own. Driven by a real Chromium over the DevTools protocol
// (chromedp, pure Go). "Headless" only means "without a visible window" — the
// same engine, full rendering; the "seeing" is done by the screenshot.
//
// Everything runs locally in the sandbox daemon (Descriptor.NoCredentials, as
// with the dev plugin); the browser needs no secrets. Which pages it can reach
// is still gated by the org's egress allowlist — the browser is the most
// powerful egress tool, which is why ACCESS.md, activation and guard-rails
// (subject browser:<action>) apply as with any target system.
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

// shotCounter hands out consecutive default screenshot names per process.
var shotCounter atomic.Int64

func nextShotName() string {
	return fmt.Sprintf("shot-%d.png", shotCounter.Add(1))
}

// contentMax caps how much page text content returns straight into the
// session — larger pages belong in a screenshot or behind a narrow selector.
const contentMax = 20000

// actionTimeout caps a single browser action (navigation, click, …).
// Overridable via COVEY_BROWSER_TIMEOUT_SECS.
func actionTimeout() time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_BROWSER_TIMEOUT_SECS"))); err == nil && s > 0 {
		return time.Duration(s) * time.Second
	}
	return 45 * time.Second
}

// manager keeps one persistent Chromium alive across the agent's waking phase
// (cookies/tabs/login survive between actions) and serializes all runs — a
// chromedp context cannot be driven concurrently, and Covey works serially per
// agent anyway.
type manager struct {
	mu            sync.Mutex
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc
}

var super = &manager{}

func init() { target.OnShutdown(super.shutdown) }

// ensureLocked starts the browser on demand (the lock must be held) and
// returns the browser context that actions run against.
func (m *manager) ensureLocked() (context.Context, error) {
	if m.browserCtx != nil && m.browserCtx.Err() == nil {
		return m.browserCtx, nil
	}
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	// The container IS the sandbox — Chromium's own setuid sandbox needs kernel
	// caps that are missing here, so --no-sandbox is defensible. Small /dev/shm
	// in containers → --disable-dev-shm-usage. Fixed window size for
	// reproducible screenshots.
	opts = append(opts,
		chromedp.NoSandbox,
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.WindowSize(1440, 900),
	)
	if os.Getenv("COVEY_BROWSER_HEADFUL") != "" {
		// Visible mode (needs an X server/Xvfb) — for the later live-view/takeover
		// stage. The default stays headless.
		opts = append(opts, chromedp.Flag("headless", false))
	}
	if p := strings.TrimSpace(os.Getenv("COVEY_BROWSER_CHROME_PATH")); p != "" {
		opts = append(opts, chromedp.ExecPath(p))
	}
	// How long we wait for Chromium to announce its DevTools websocket.
	//
	// chromedp's default is 20 seconds, and that is too tight for a COLD start
	// on a loaded machine: the end-to-end test failed in CI roughly every other
	// run, always at exactly 20.0 s, always with "websocket url timeout
	// reached". Re-running turned it green, which is the signature of a bound
	// that is merely too small — and re-running a red build until it passes is
	// not a fix, it is a habit that eventually hides a real failure.
	//
	// Tied to the action timeout on purpose: whoever grants a slow environment
	// more time per action means the browser too, and a start that takes longer
	// than a whole action is broken in a way no bound should paper over.
	opts = append(opts, chromedp.WSURLReadTimeout(actionTimeout()))

	// Detached from any single action: the session is the whole point of this
	// plugin — cookies and login are meant to survive across several actions.
	// It is ended via allocCancel/browserCancel (Close), not via an inherited
	// context.
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	// An empty run forces the browser to start and binds the initial tab to the
	// long-lived browserCtx — NOT to a timeout child, whose cancel() would tear
	// the tab down again right away. A missing Chromium surfaces here as a clear
	// error (instead of only on the first navigation).
	if err := chromedp.Run(browserCtx); err != nil {
		browserCancel()
		allocCancel()
		return nil, fmt.Errorf("chromium start (is chromium installed in the sandbox image?): %w", err)
	}
	m.allocCancel, m.browserCtx, m.browserCancel = allocCancel, browserCtx, browserCancel
	return m.browserCtx, nil
}

// do runs a series of chromedp actions atomically and serialized.
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

// shutdown terminates the browser when the sandbox shuts down — otherwise the
// Chromium process would outlive the waking phase.
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
