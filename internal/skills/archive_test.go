package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const pdfSkill = "---\nname: pdf\ndescription: Work with PDFs. Use when a PDF arrives.\nlicense: MIT\n---\n# PDF\n"

// A repository archive: GitHub wraps it in one top-level directory and the
// skills sit somewhere below, with a binary asset and OS litter beside them.
func TestFromZipRepository(t *testing.T) {
	data := zipOf(t, map[string]string{
		"repo-main/README.md":                      "readme",
		"repo-main/skills/pdf/SKILL.md":            pdfSkill,
		"repo-main/skills/pdf/scripts/run.sh":      "#!/bin/sh\necho hi\n",
		"repo-main/skills/pdf/assets/logo.png":     "\x89PNG\r\n\x1a\n\x00\x00binary",
		"repo-main/skills/pdf/references/SKILL.md": "nested — belongs to pdf",
		"repo-main/skills/pdf/.DS_Store":           "litter",
		"__MACOSX/repo-main/._x":                   "litter",
		"repo-main/skills/docx/SKILL.md":           "---\nname: docx\ndescription: Word files.\n---\nBody",
	})
	got, err := FromZip(data, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Spec.Name != "docx" || got[1].Spec.Name != "pdf" {
		t.Fatalf("expected docx and pdf, got %+v", got)
	}
	pdf := got[1]
	if len(pdf.Problems) != 0 {
		t.Fatalf("pdf must be valid: %v", pdf.Problems)
	}
	if pdf.Spec.License != "MIT" {
		t.Errorf("license lost: %+v", pdf.Spec.Meta)
	}
	paths := map[string]File{}
	for _, f := range pdf.Spec.Files {
		paths[f.Path] = f
	}
	if len(paths) != 4 {
		t.Errorf("expected SKILL.md, scripts/run.sh, assets/logo.png, references/SKILL.md, got %v", paths)
	}
	if !paths["assets/logo.png"].Binary() {
		t.Error("the PNG must be stored as a binary file")
	}
	if paths["scripts/run.sh"].Binary() {
		t.Error("the script must stay text")
	}
	if strings.Contains(paths[EntryFile].Content, "license:") {
		t.Error("the frontmatter must be stripped from the stored SKILL.md")
	}

	// subdir picks one skill, matched after the top-level directory.
	only, err := FromZip(data, "skills/pdf")
	if err != nil || len(only) != 1 || only[0].Spec.Name != "pdf" {
		t.Fatalf("subdir filter wrong: %+v %v", only, err)
	}
	if _, err := FromZip(data, "skills/none"); err == nil {
		t.Fatal("a subdir without skills must be an error")
	}
}

// A single skill zipped at the top level takes its name from the frontmatter;
// a directory whose name differs from the frontmatter is reported, not
// silently renamed.
func TestFromZipNamesAndProblems(t *testing.T) {
	got, err := FromZip(zipOf(t, map[string]string{"SKILL.md": pdfSkill}), "")
	if err != nil || len(got) != 1 || got[0].Spec.Name != "pdf" || len(got[0].Problems) != 0 {
		t.Fatalf("top-level skill read wrong: %+v %v", got, err)
	}

	got, err = FromZip(zipOf(t, map[string]string{"other/SKILL.md": pdfSkill}), "")
	if err != nil || len(got) != 1 || !hasProblem(got[0], "does not match directory") {
		t.Fatalf("name mismatch must be reported: %+v %v", got, err)
	}

	got, err = FromZip(zipOf(t, map[string]string{"x/SKILL.md": "---\nname: x\nmodel: opus\ndescription: d\n---\n"}), "")
	if err != nil || !hasProblem(got[0], "model") {
		t.Fatalf("unknown key must be reported: %+v %v", got, err)
	}

	got, err = FromZip(zipOf(t, map[string]string{"x/SKILL.md": "# no frontmatter\n"}), "")
	if err != nil || !hasProblem(got[0], "description missing") {
		t.Fatalf("a missing description must be reported: %+v %v", got, err)
	}

	if _, err := FromZip([]byte("not a zip"), ""); err == nil {
		t.Fatal("garbage must be refused")
	}
	if _, err := FromZip(zipOf(t, map[string]string{"README.md": "x"}), ""); err == nil {
		t.Fatal("an archive without SKILL.md must be refused")
	}
}

func hasProblem(c Candidate, s string) bool {
	for _, p := range c.Problems {
		if strings.Contains(p, s) {
			return true
		}
	}
	return false
}

func TestFromFS(t *testing.T) {
	fsys := fstest.MapFS{
		"pdf/SKILL.md":       {Data: []byte(pdfSkill)},
		"pdf/scripts/run.py": {Data: []byte("print(1)\n")},
		".git/config":        {Data: []byte("x")},
	}
	got, err := FromFS(fsys)
	if err != nil || len(got) != 1 || got[0].Spec.Name != "pdf" || len(got[0].Spec.Files) != 2 {
		t.Fatalf("FromFS wrong: %+v %v", got, err)
	}
}

// A symbolic link in a skill directory would read a file from outside it.
func TestFromFSSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "pdf"), 0o755)
	os.WriteFile(filepath.Join(dir, "pdf", "SKILL.md"), []byte(pdfSkill), 0o644)
	os.WriteFile(filepath.Join(dir, "secret"), []byte("x"), 0o644)
	if err := os.Symlink(filepath.Join(dir, "secret"), filepath.Join(dir, "pdf", "link")); err != nil {
		t.Skip("no symlinks here")
	}
	got, err := FromFS(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range got[0].Spec.Files {
		if f.Path == "link" {
			t.Fatal("the symbolic link was read")
		}
	}
	if len(got[0].Skipped) != 1 {
		t.Fatalf("the link must be reported as skipped: %+v", got[0].Skipped)
	}
}

func TestResolveSource(t *testing.T) {
	for _, tc := range []struct{ in, url, sub string }{
		{"https://github.com/acme/skills", "https://codeload.github.com/acme/skills/zip/HEAD", ""},
		{"https://github.com/acme/skills.git", "https://codeload.github.com/acme/skills/zip/HEAD", ""},
		{"https://github.com/acme/skills/tree/main/skills/pdf", "https://codeload.github.com/acme/skills/zip/main", "skills/pdf"},
		{"https://github.com/acme/skills/blob/v1/skills/pdf/SKILL.md", "https://codeload.github.com/acme/skills/zip/v1", "skills/pdf"},
		{"https://gitlab.example.org/group/sub/proj/-/tree/main/skills/pdf",
			"https://gitlab.example.org/group/sub/proj/-/archive/main/proj-main.zip?path=skills%2Fpdf", "skills/pdf"},
		{"https://example.org/pdf.zip", "https://example.org/pdf.zip", ""},
	} {
		src, err := ResolveSource(tc.in)
		if err != nil || src.ArchiveURL != tc.url || src.Subdir != tc.sub {
			t.Errorf("%s → %+v %v, expected %s / %s", tc.in, src, err, tc.url, tc.sub)
		}
	}
	for _, bad := range []string{"http://example.org/x.zip", "file:///etc/passwd", "not a url", "https://github.com/acme"} {
		if _, err := ResolveSource(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestPublicIP(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", "0.0.0.0", "::1", "fc00::1", "fe80::1", "64:ff9b::a00:1"} {
		if PublicIP(net.ParseIP(s)) {
			t.Errorf("%s must not count as public", s)
		}
	}
	for _, s := range []string{"140.82.112.3", "2606:4700::1111"} {
		if !PublicIP(net.ParseIP(s)) {
			t.Errorf("%s must count as public", s)
		}
	}
}

// The guarded client refuses to connect to a loopback server even when the
// URL is otherwise fine — the request must not reach the control plane's own
// network.
func TestGuardedClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(zipOf(t, map[string]string{"SKILL.md": pdfSkill}))
	}))
	defer srv.Close()
	if _, _, err := (Fetcher{}).Fetch(context.Background(), srv.URL+"/a.zip"); err == nil ||
		!strings.Contains(err.Error(), "not public") {
		t.Fatalf("loopback must be refused, got %v", err)
	}
	// With a client that may reach it, the same URL works — the refusal above
	// was the guard, not the URL.
	data, sub, err := (Fetcher{Client: srv.Client()}).Fetch(context.Background(), srv.URL+"/a.zip")
	if err != nil || sub != "" || !bytes.HasPrefix(data, []byte("PK")) {
		t.Fatalf("fetch failed: %v", err)
	}
}
