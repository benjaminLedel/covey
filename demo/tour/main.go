// demo-tour drives through the interface of a covey instance once and writes
// an image at each stop: the screenshots for the README and the frames for
// the demo GIF.
//
// Why a program and not by hand: the images in the README go stale with
// every change to the interface, and hand-shot images are cropped
// differently, scrolled differently, a different width every time. Here the
// crop stands in the code — after a UI change run it once and all images
// agree again.
//
// It is meant for the demo instance from demo/seed, not for an instance with
// real data: what is captured here ends up publicly in the README.
//
//	go run ./demo/tour -url http://localhost:8495 -out /tmp/tour
//	python3 demo/tour/build.py /tmp/tour        # Bilder + GIF ins Repo
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// One stop of the tour. Either an address (nav) or a click on a tab
// (tab) — the tabs of the agent page do not stand in the URL.
type stop struct {
	name     string // filename without extension; also the name in the README
	nav      string // path, empty = stay on the page
	tab      string // label of the tab to click
	click    string // XPath to click before the capture
	wait     string // selector that must be present before the shot
	scroll   int    // pixels to scroll before the capture
	holdMS   int    // how long the image stays in the GIF
	inREADME bool   // needed as a JPEG under web/public/shots/
}

func main() {
	base := flag.String("url", "http://localhost:8495", "Adresse der Demo-Instanz")
	out := flag.String("out", "tour-out", "Zielverzeichnis für die Einzelbilder")
	lang := flag.String("lang", "en", "Sprache der Oberfläche (en|de)")
	width := flag.Int("width", 1280, "Breite des Ausschnitts")
	height := flag.Int("height", 800, "Höhe des Ausschnitts")
	flag.Parse()

	if err := run(*base, *out, *lang, *width, *height); err != nil {
		log.Fatalf("tour: %v", err)
	}
}

func run(base, out, lang string, width, height int) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.WindowSize(width, height),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("force-color-profile", "srgb"),
		chromedp.Flag("font-render-hinting", "none"),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTimeout := context.WithTimeout(ctx, 3*time.Minute)
	defer cancelTimeout()

	// Sign in through the demo button the sign-in card shows on localhost —
	// that way no password stands in this program.
	log.Printf("anmelden an %s", base)
	if err := chromedp.Run(ctx,
		chromedp.EmulateViewport(int64(width), int64(height)),
		chromedp.Navigate(base+"/en/sign-in"),
		chromedp.WaitVisible(`//button[contains(., 'Demo Login')]`),
		chromedp.Click(`//button[contains(., 'Demo Login')]`),
		chromedp.WaitVisible(`a[href="/costs"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("anmelden: %w", err)
	}

	// Set the language and hide the first-steps list: it is
	// aimed at a fresh instance and tells the wrong story in the
	// screenshot.
	if err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`localStorage.setItem("covey.lang", %q);`+
			`localStorage.setItem("covey.onboarding.dismissed", "1")`, lang), nil),
		chromedp.Reload(),
		chromedp.WaitVisible(`a[href="/costs"]`, chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("sprache setzen: %w", err)
	}

	adaID, err := agentID(ctx, "ada")
	if err != nil {
		return err
	}

	// The tabs carry translated labels — what gets clicked is what stands in
	// the set language (web/src/locales/*.json).
	tabMemory := "Memory"
	if lang == "de" {
		tabMemory = "Gedächtnis"
	}

	stops := []stop{
		{name: "agents", nav: "/", wait: `a[href^="/agents/"]`, holdMS: 2600, inREADME: true},
		// The board stands below the "New task" form — without scrolling the
		// image shows an empty input field instead of the work.
		{name: "backlog", nav: "/agents/" + adaID, wait: `.kanban`, scroll: 105, holdMS: 2800, inREADME: true},
		{name: "recording", tab: "Recording", wait: `.card`, holdMS: 3000},
		// Open a page: otherwise the reading area is empty, and it is exactly
		// that area which shows the memory is readable text and not vector soup.
		{name: "memory", tab: tabMemory, click: `//*[contains(text(), 'Known issue')]`,
			wait: `.wiki-group`, holdMS: 2800, inREADME: true},
		{name: "org", nav: "/org", wait: `.org-legend`, holdMS: 2800, inREADME: true},
		{name: "costs", nav: "/costs", wait: `.card`, holdMS: 2800, inREADME: true},
	}

	type frame struct {
		File     string `json:"file"`
		Name     string `json:"name"`
		HoldMS   int    `json:"hold_ms"`
		InREADME bool   `json:"in_readme"`
	}
	var frames []frame

	for i, st := range stops {
		actions := []chromedp.Action{}
		if st.nav != "" {
			actions = append(actions, chromedp.Navigate(base+st.nav))
		}
		if st.tab != "" {
			actions = append(actions, chromedp.Click(
				fmt.Sprintf(`//button[normalize-space()=%q]`, st.tab)))
		}
		if st.click != "" {
			actions = append(actions, chromedp.Click(st.click))
		}
		if st.wait != "" {
			actions = append(actions, chromedp.WaitVisible(st.wait, chromedp.ByQuery))
		}
		if st.scroll != 0 {
			actions = append(actions, chromedp.Evaluate(
				fmt.Sprintf(`window.scrollTo({top: %d})`, st.scroll), nil))
		}
		// Let it settle: charts animate while building, and an image in the
		// middle of the animation looks like a rendering fault.
		actions = append(actions, chromedp.Sleep(1600*time.Millisecond))

		var buf []byte
		actions = append(actions, chromedp.CaptureScreenshot(&buf))
		if err := chromedp.Run(ctx, actions...); err != nil {
			return fmt.Errorf("station %s: %w", st.name, err)
		}
		file := fmt.Sprintf("%02d-%s.png", i+1, st.name)
		if err := os.WriteFile(filepath.Join(out, file), buf, 0o644); err != nil {
			return err
		}
		log.Printf("  %s (%d kB)", file, len(buf)/1024)
		frames = append(frames, frame{File: file, Name: st.name, HoldMS: st.holdMS, InREADME: st.inREADME})
	}

	manifest, err := json.MarshalIndent(map[string]any{
		"lang": lang, "width": width, "height": height, "frames": frames,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "tour.json"), manifest, 0o644)
}

// agentID looks up an agent's identifier via its slug — the identifiers are
// new with every seed, so only the slug may stand in code.
func agentID(ctx context.Context, slug string) (string, error) {
	var id string
	err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf(`
		fetch("/api/v1/agents", {credentials:"include"})
			.then(r => r.json())
			.then(l => (l.find(a => a.slug === %q) || {}).id || "")
	`, slug), &id, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	}))
	if err != nil {
		return "", fmt.Errorf("Agent %q suchen: %w", slug, err)
	}
	if id == "" {
		return "", fmt.Errorf("Agent %q nicht gefunden — ist der Seed gelaufen?", slug)
	}
	return id, nil
}
