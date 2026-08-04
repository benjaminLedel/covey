// demo-tour fährt die Oberfläche einer Covey-Instanz einmal ab und legt von
// jeder Station ein Bild ab: die Screenshots für das README und die Einzelbilder
// für das Demo-GIF.
//
// Warum als Programm und nicht von Hand: die Bilder im README veralten mit jeder
// Änderung an der Oberfläche, und von Hand geschossene Bilder sind jedes Mal
// anders ausgeschnitten, anders gescrollt, anders breit. Hier steht der
// Ausschnitt im Code — nach einer UI-Änderung einmal laufen lassen und alle
// Bilder stimmen wieder überein.
//
// Gedacht ist es für die Demo-Instanz aus demo/seed, nicht für eine Instanz mit
// echten Daten: was hier aufgenommen wird, landet öffentlich im README.
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

// Eine Station der Tour. Entweder eine Adresse (nav) oder ein Klick auf einen
// Reiter (tab) — die Reiter der Agentenseite stehen nicht in der URL.
type stop struct {
	name     string // Dateiname ohne Endung; zugleich der Name im README
	nav      string // Pfad, leer = auf der Seite bleiben
	tab      string // Beschriftung des zu klickenden Reiters
	click    string // XPath, auf den vor der Aufnahme geklickt wird
	wait     string // Selektor, der da sein muss, bevor geschossen wird
	scroll   int    // Pixel, um die vor der Aufnahme gescrollt wird
	holdMS   int    // wie lange das Bild im GIF stehen bleibt
	inREADME bool   // wird als JPEG unter web/public/shots/ gebraucht
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

	// Anmelden über den Demo-Knopf, den die Anmeldekarte auf localhost zeigt —
	// so steht in diesem Programm kein Passwort.
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

	// Sprache festlegen und die Erste-Schritte-Liste ausblenden: sie richtet
	// sich an eine frische Instanz und erzählt im Screenshot die falsche
	// Geschichte.
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

	// Die Reiter tragen übersetzte Beschriftungen — angeklickt wird, was in
	// der eingestellten Sprache dasteht (web/src/locales/*.json).
	tabMemory := "Memory"
	if lang == "de" {
		tabMemory = "Gedächtnis"
	}

	stops := []stop{
		{name: "agents", nav: "/", wait: `a[href^="/agents/"]`, holdMS: 2600, inREADME: true},
		// Das Board steht unter dem Formular "Neue Aufgabe" — ohne Scrollen
		// zeigt das Bild ein leeres Eingabefeld statt der Arbeit.
		{name: "backlog", nav: "/agents/" + adaID, wait: `.kanban`, scroll: 105, holdMS: 2800, inREADME: true},
		{name: "recording", tab: "Recording", wait: `.card`, holdMS: 3000},
		// Eine Seite aufschlagen: der Lesebereich ist sonst leer, und gerade er
		// zeigt, dass das Gedächtnis lesbarer Text ist und keine Vektorsuppe.
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
		// Ruhen lassen: Diagramme animieren beim Aufbau, und ein Bild mitten
		// in der Animation sieht aus wie ein Darstellungsfehler.
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

// agentID sucht die Kennung eines Agenten über seinen slug — die Kennungen sind
// bei jedem Seed neu, im Code stehen darf also nur der slug.
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
