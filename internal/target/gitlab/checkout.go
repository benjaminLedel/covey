package gitlab

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// preserveDirs bleiben bei einem erneuten Checkout erhalten (nicht mit dem
// Quellcode weggewischt): Dependency- und Build-Caches, die sonst jeden Lauf
// neu aufgebaut werden müssten. So ist ein Folge-Checkout auf demselben Ref
// inkrementell (npm/pip/go finden ihren Cache vor) statt kalt.
var preserveDirs = map[string]bool{
	"node_modules": true, ".venv": true, "venv": true, "vendor": true,
	"target": true, ".gradle": true, ".next": true, ".cache": true,
	".pnpm-store": true, ".yarn": true,
}

var refSanitize = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

// stableCheckoutDir liefert einen über Commits stabilen Verzeichnisnamen pro
// (Projekt, Ref, subPath) — anders als der GitLab-Archiv-Top-Level, der die SHA
// enthält und sich mit jedem Commit ändert (dann käme node_modules nie mit).
func stableCheckoutDir(projectID int, ref, subPath string) string {
	slug := func(s string) string {
		s = strings.Trim(refSanitize.ReplaceAllString(strings.TrimSpace(s), "-"), "-")
		if len(s) > 48 {
			s = strings.Trim(s[:48], "-")
		}
		return s
	}
	r := slug(ref)
	if r == "" {
		r = "default"
	}
	name := fmt.Sprintf("p%d-%s", projectID, r)
	if sp := slug(subPath); sp != "" {
		name += "-" + sp
	}
	return name
}

// pruneExceptPreserved leert ein Verzeichnis, lässt aber die Cache-Verzeichnisse
// aus preserveDirs stehen — so wird der Quellcode ersetzt, der Cache bleibt.
func pruneExceptPreserved(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() && preserveDirs[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// checkoutMaxBytes begrenzt die entpackte Gesamtgröße eines Checkouts —
// Schutz der Sandbox vor Riesen-Repos und Zip-Bomben. Default 512 MB,
// überschreibbar via COVEY_GITLAB_CHECKOUT_MAX_MB (Prozess-Env des Daemons).
func checkoutMaxBytes() int64 {
	if mb, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_GITLAB_CHECKOUT_MAX_MB"))); err == nil && mb > 0 {
		return int64(mb) << 20
	}
	return 512 << 20
}

// CheckoutResult ist die Antwort der checkout-Aktion an den Agenten: wo der
// Code liegt und wie er damit weiterarbeitet.
type CheckoutResult struct {
	Path    string `json:"path"`
	Ref     string `json:"ref,omitempty"`
	SubPath string `json:"sub_path,omitempty"`
	Files   int    `json:"files"`
	Hint    string `json:"hint"`
}

// Checkout materialisiert den Quellcode eines Projekts in der Sandbox: lädt
// das Repository-Archiv über die API (das gebrokerte Token bleibt im Daemon,
// es landet nie im Dateisystem — anders als bei einem git clone mit
// Credential-Remote) und entpackt es unter <workdir>/repos/. subPath schränkt
// auf ein Unterverzeichnis ein (Teil-Checkout für große Repos). Ein
// vorhandener Stand desselben Archivs wird ersetzt — der Agent arbeitet
// immer auf dem aktuellen Code.
func Checkout(ctx context.Context, gc *Client, projectID int, ref, subPath, workdir string) (CheckoutResult, error) {
	if workdir == "" {
		return CheckoutResult{}, fmt.Errorf("checkout braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	body, err := gc.DownloadArchive(ctx, projectID, ref, subPath)
	if err != nil {
		return CheckoutResult{}, err
	}
	defer body.Close()

	destDir := filepath.Join(workdir, "repos", stableCheckoutDir(projectID, ref, subPath))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return CheckoutResult{}, err
	}
	// Alten Quellcode entfernen, Caches (preserveDirs) stehen lassen.
	if err := pruneExceptPreserved(destDir); err != nil {
		return CheckoutResult{}, err
	}
	files, err := extractTarGzInto(body, destDir)
	if err != nil {
		return CheckoutResult{}, err
	}
	initGitBaseline(ctx, destDir)
	return CheckoutResult{
		Path:    destDir,
		Ref:     ref,
		SubPath: subPath,
		Files:   files,
		Hint:    "Quellcode liegt lokal — durchsuche und lies ihn direkt (Grep/Read/Bash). Für die eigentliche Änderung übergib den Pfad an dev agent: Der Sub-Agent arbeitet IM Projekt und bekommt dort dessen eigene Regeln (CLAUDE.md, .claude/agents, Skills) — du selbst siehst die nicht. Das Verzeichnis ist ein git-Repo mit dem Upstream-Stand als Baseline-Commit, geänderte Dateien meldet der Sub-Agent zurück. Dependency-Caches (node_modules o. ä.) bleiben über Läufe erhalten, npm/pip/go install läuft dann inkrementell.",
	}, nil
}

// initGitBaseline legt im frischen Checkout ein git-Repository mit genau einem
// Commit an: dem gerade entpackten Upstream-Stand. Das Archiv selbst bringt
// keine .git mit (es ist ein Tarball, kein Klon), und daraus folgen zwei
// Probleme, die dieser Commit löst:
//
//   - Werkzeuge und Skripte des Projekts, die git aufrufen, scheitern sonst.
//   - Nach der Arbeit im Checkout ließe sich sonst nicht sagen, WAS geändert
//     wurde — die commit-Aktion braucht aber genau diese Dateiliste.
//
// Die Baseline wird bei jedem Checkout neu gezogen (.git steht bewusst NICHT
// in preserveDirs): Nur so entspricht sie exakt dem frischen Upstream-Stand,
// und `git status` zeigt danach ausschließlich die eigene Arbeit.
//
// Alles hier ist best effort — fehlt git oder scheitert ein Schritt, bleibt der
// Checkout gültig; der Agent verliert nur den Komfort.
func initGitBaseline(ctx context.Context, dir string) {
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		// Identität als Flag statt via git config: Wir fassen weder die globale
		// Konfiguration des Sandbox-Users an noch brauchen wir eine echte.
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Covey", "GIT_AUTHOR_EMAIL=covey@localhost",
			"GIT_COMMITTER_NAME=Covey", "GIT_COMMITTER_EMAIL=covey@localhost")
		return cmd.Run()
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		return
	}
	if err := git("init", "-q"); err != nil {
		return
	}
	// Dependency- und Build-Caches (preserveDirs) überleben den Checkout und
	// gehören nicht zur Arbeit. Über info/exclude bleiben sie aus Baseline und
	// späterem `git status` heraus, ohne die .gitignore des Projekts anzufassen.
	var excl strings.Builder
	for name := range preserveDirs {
		excl.WriteString("/" + name + "\n")
	}
	_ = os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(excl.String()), 0o644)
	if err := git("add", "-A"); err != nil {
		return
	}
	_ = git("commit", "-q", "--allow-empty", "-m", "covey baseline")
}

// extractTarGz entpackt ein GitLab-Repository-Archiv nach destRoot und liefert
// den Namen des Top-Level-Verzeichnisses (GitLab: <projekt>-<ref>-<sha>).
// Sicherheit: Pfade werden gegen Traversal geprüft, Symlinks übersprungen,
// die entpackte Gesamtgröße ist begrenzt.
func extractTarGz(r io.Reader, destRoot string) (topDir string, files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", 0, fmt.Errorf("archiv lesen: %w", err)
	}
	defer gz.Close()

	maxBytes := checkoutMaxBytes()
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("archiv lesen: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "pax_global_header" {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return "", 0, fmt.Errorf("unsicherer pfad im archiv: %q", hdr.Name)
		}
		if topDir == "" {
			topDir = strings.SplitN(name, string(filepath.Separator), 2)[0]
			// Vorherigen Stand desselben Archivs ersetzen (frischer Checkout).
			if err := os.RemoveAll(filepath.Join(destRoot, topDir)); err != nil {
				return "", 0, err
			}
		}
		dest := filepath.Join(destRoot, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return "", 0, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxBytes {
				return "", 0, fmt.Errorf("archiv größer als %d MB — nutze checkout mit \"path\" (Unterverzeichnis) oder navigiere mit list_tree und lies gezielt per read_file; das Limit setzt COVEY_GITLAB_CHECKOUT_MAX_MB", maxBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return "", 0, err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return "", 0, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxBytes)); err != nil {
				f.Close()
				return "", 0, err
			}
			f.Close()
			files++
		default:
			// Symlinks & Sonstiges: für die Code-Lektüre unnötig, als
			// Ausbruchsvektor riskant — bewusst überspringen.
		}
	}
	if topDir == "" {
		return "", 0, fmt.Errorf("leeres archiv")
	}
	return topDir, files, nil
}

// extractTarGzInto entpackt ein GitLab-Repository-Archiv in destDir und strippt
// dabei das Top-Level-Verzeichnis (die SHA-behaftete Hülle), sodass der Inhalt
// direkt in destDir liegt — Voraussetzung für ein stabiles, cache-erhaltendes
// Zielverzeichnis. Anders als extractTarGz räumt es destDir NICHT ab (der Aufrufer
// prunt cache-schonend). Sicherheit wie extractTarGz: Traversal-Schutz, Symlinks
// übersprungen, Gesamtgröße begrenzt.
func extractTarGzInto(r io.Reader, destDir string) (files int, err error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return 0, fmt.Errorf("archiv lesen: %w", err)
	}
	defer gz.Close()

	maxBytes := checkoutMaxBytes()
	var total int64
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("archiv lesen: %w", err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." || name == "pax_global_header" {
			continue
		}
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			return 0, fmt.Errorf("unsicherer pfad im archiv: %q", hdr.Name)
		}
		// Top-Level-Verzeichnis (projname-ref-sha) abstreifen.
		rel := strings.SplitN(name, string(filepath.Separator), 2)
		if len(rel) < 2 || rel[1] == "" {
			continue // das Hüllverzeichnis selbst
		}
		dest := filepath.Join(destDir, rel[1])
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return 0, err
			}
		case tar.TypeReg:
			total += hdr.Size
			if total > maxBytes {
				return 0, fmt.Errorf("archiv größer als %d MB — nutze checkout mit \"path\" (Unterverzeichnis) oder navigiere mit list_tree und lies gezielt per read_file; das Limit setzt COVEY_GITLAB_CHECKOUT_MAX_MB", maxBytes>>20)
			}
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return 0, err
			}
			f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return 0, err
			}
			if _, err := io.Copy(f, io.LimitReader(tr, maxBytes)); err != nil {
				f.Close()
				return 0, err
			}
			f.Close()
			files++
		default:
			// Symlinks & Sonstiges bewusst überspringen.
		}
	}
	return files, nil
}
