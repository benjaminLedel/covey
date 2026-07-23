package gitlab

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Größen-Limits für die commit-Aktion: eine einzelne Datei und die Summe
// aller Dateien eines Commits — die Commits-API transportiert Inhalte im
// JSON-Body, Riesen-Commits gehören nicht in diesen Weg.
const (
	maxCommitFileBytes  = 4 << 20  // 4 MB je Datei
	maxCommitTotalBytes = 16 << 20 // 16 MB je Commit
)

// CommitResult ist die Antwort der commit-Aktion: was gepusht wurde und wie
// es weitergeht (Merge Request eröffnen).
type CommitResult struct {
	Branch        string   `json:"branch"`
	BranchCreated bool     `json:"branch_created"`
	Commit        Commit   `json:"commit"`
	Files         []string `json:"files"`
	Deleted       []string `json:"deleted,omitempty"`
	Hint          string   `json:"hint"`
}

// CommitFromCheckout pusht lokal editierte Dateien aus dem Sandbox-Checkout
// als einen Commit auf einen Feature-Branch — über die Commits-API, damit das
// gebrokerte Token im Daemon bleibt (kein git-Remote mit Credentials in der
// Sandbox). Existiert der Branch noch nicht, wird er vom Start-Branch
// (Default: Default-Branch des Projekts) abgezweigt. Direkte Commits auf den
// Default-Branch sind fail-closed verboten — der Weg in den Hauptzweig führt
// ausschließlich über einen Merge Request.
func CommitFromCheckout(ctx context.Context, gc *Client, projectID int, branch, startBranch, message, checkoutPath string, files, deleted []string, workdir string) (CommitResult, error) {
	if workdir == "" {
		return CommitResult{}, fmt.Errorf("commit braucht eine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.TrimSpace(message) == "" {
		return CommitResult{}, fmt.Errorf("branch oder message fehlt")
	}
	if len(files)+len(deleted) == 0 {
		return CommitResult{}, fmt.Errorf("files (und/oder deleted) fehlt — nichts zu committen")
	}

	proj, err := gc.GetProject(ctx, projectID)
	if err != nil {
		return CommitResult{}, err
	}
	if proj.DefaultBranch != "" && branch == proj.DefaultBranch {
		return CommitResult{}, fmt.Errorf("direkter Commit auf den Default-Branch %q ist nicht erlaubt — arbeite auf einem Feature-Branch und eröffne einen Merge Request (create_merge_request)", branch)
	}

	// Der Checkout-Pfad muss innerhalb der Sandbox liegen — die Aktion liest
	// Dateien aus dem Dateisystem des Daemons und darf nur sehen, was der
	// Checkout dorthin materialisiert hat.
	root, err := filepath.Abs(filepath.Clean(checkoutPath))
	if err != nil || checkoutPath == "" {
		return CommitResult{}, fmt.Errorf("checkout_path fehlt oder ist ungültig — nutze den Pfad aus dem checkout-Ergebnis")
	}
	absWork, err := filepath.Abs(workdir)
	if err != nil {
		return CommitResult{}, err
	}
	if root != absWork && !strings.HasPrefix(root, absWork+string(filepath.Separator)) {
		return CommitResult{}, fmt.Errorf("checkout_path %q liegt außerhalb der Sandbox", checkoutPath)
	}

	// Branch schon vorhanden? Dann committen wir obendrauf (kein start_branch);
	// sonst zweigt die Commits-API ihn vom Start-Branch ab.
	if startBranch = strings.TrimSpace(startBranch); startBranch == "" {
		startBranch = proj.DefaultBranch
	}
	branchExists := false
	if bs, err := gc.ListBranches(ctx, projectID, branch); err == nil {
		for _, b := range bs {
			if b.Name == branch {
				branchExists = true
				break
			}
		}
	}
	baseRef := startBranch
	if branchExists {
		baseRef = branch
	}

	var actions []CommitAction
	var total int64
	for _, f := range files {
		rel, err := repoRelPath(f)
		if err != nil {
			return CommitResult{}, err
		}
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return CommitResult{}, fmt.Errorf("datei %q im checkout lesen: %w", rel, err)
		}
		if len(data) > maxCommitFileBytes {
			return CommitResult{}, fmt.Errorf("datei %q ist größer als %d MB — solche Dateien gehören nicht in diesen Commit-Weg", rel, maxCommitFileBytes>>20)
		}
		if total += int64(len(data)); total > maxCommitTotalBytes {
			return CommitResult{}, fmt.Errorf("commit größer als %d MB — teile die Änderung in mehrere Commits", maxCommitTotalBytes>>20)
		}
		act := "create"
		if exists, err := gc.FileExists(ctx, projectID, rel, baseRef); err == nil && exists {
			act = "update"
		}
		actions = append(actions, CommitAction{
			Action:   act,
			FilePath: filepath.ToSlash(rel),
			Content:  base64.StdEncoding.EncodeToString(data),
			Encoding: "base64",
		})
	}
	for _, f := range deleted {
		rel, err := repoRelPath(f)
		if err != nil {
			return CommitResult{}, err
		}
		actions = append(actions, CommitAction{Action: "delete", FilePath: filepath.ToSlash(rel)})
	}

	start := ""
	if !branchExists {
		start = startBranch
	}
	commit, err := gc.CommitFiles(ctx, projectID, branch, start, message, actions)
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{
		Branch:        branch,
		BranchCreated: !branchExists,
		Commit:        commit,
		Files:         files,
		Deleted:       deleted,
		Hint:          fmt.Sprintf("Gepusht auf Branch %q. Eröffne jetzt den Merge Request: create_merge_request {\"project_id\":%d,\"source_branch\":%q,...} — Reviewer ist dein Vorgesetzter.", branch, projectID, branch),
	}, nil
}

// repoRelPath prüft einen vom Agenten gelieferten Dateipfad: repo-relativ,
// ohne Absolutpfad und ohne Traversal nach oben — er wird sowohl lokal
// (Lesen aus dem Checkout) als auch remote (file_path im Commit) verwendet.
func repoRelPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	clean := filepath.Clean(filepath.FromSlash(p))
	if p == "" || clean == "." || filepath.IsAbs(clean) ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("ungültiger dateipfad %q — erwartet wird ein repo-relativer Pfad", p)
	}
	return clean, nil
}
