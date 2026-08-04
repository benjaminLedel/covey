package httpapi

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestDownloadSkill covers the skill download: a valid ZIP with the skill files
// under covey-agent/, and the instance's base URL injected into SKILL.md (so
// that the downloaded skill works against THIS instance).
func TestDownloadSkill(t *testing.T) {
	s := &Server{SiteURL: "https://covey.example.test"}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/skills/covey-agent.zip", nil)

	s.handleDownloadSkill(rec, req)

	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a valid ZIP: %v", err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = string(data)
	}
	if _, ok := files["covey-agent/SKILL.md"]; !ok {
		t.Fatalf("SKILL.md missing from the ZIP; contained: %v", keys(files))
	}
	if _, ok := files["covey-agent/reference.md"]; !ok {
		t.Fatalf("reference.md missing from the ZIP; contained: %v", keys(files))
	}
	if !strings.Contains(files["covey-agent/SKILL.md"], "https://covey.example.test") {
		t.Fatalf("instance URL not injected into SKILL.md")
	}
	// The frontmatter has to survive (the skill stays valid).
	if !strings.HasPrefix(files["covey-agent/SKILL.md"], "---\n") {
		t.Fatalf("YAML frontmatter of SKILL.md destroyed")
	}
}

// TestDownloadSkillFallbackHost: without PublicURL the host origin is used.
func TestDownloadSkillFallbackHost(t *testing.T) {
	s := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "http://covey.local:8494/api/v1/skills/covey-agent.zip", nil)
	s.handleDownloadSkill(rec, req)

	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("not a valid ZIP: %v", err)
	}
	for _, f := range zr.File {
		if f.Name == "covey-agent/SKILL.md" {
			rc, _ := f.Open()
			data, _ := io.ReadAll(rc)
			rc.Close()
			if !strings.Contains(string(data), "covey.local:8494") {
				t.Fatalf("fallback host not injected")
			}
			return
		}
	}
	t.Fatal("SKILL.md not found")
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
