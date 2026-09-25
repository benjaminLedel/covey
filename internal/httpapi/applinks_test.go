package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"covey/internal/config"
)

// Without configuration an installation claims nothing (#333): the files
// belong to whoever ships the app.
func TestAppLinksAreOffUnlessConfigured(t *testing.T) {
	for _, s := range []*Server{{}, {Config: &config.Config{AndroidAppPackage: "work.covey.covey_mobile"}}} {
		for _, h := range []http.HandlerFunc{s.handleAppleAppSiteAssociation, s.handleAssetLinks} {
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodGet, "/.well-known/x", nil))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("unconfigured: want 404, got %d", rec.Code)
			}
		}
	}
}

func TestAppLinksClaimPairAndTeamOnly(t *testing.T) {
	s := &Server{Config: &config.Config{
		AppleAppID:        "ABCDE12345.work.covey.coveyMobile",
		AndroidAppPackage: "work.covey.covey_mobile",
		AndroidCertSHA256: []string{"AA:BB"},
	}}

	rec := httptest.NewRecorder()
	s.handleAppleAppSiteAssociation(rec, httptest.NewRequest(http.MethodGet, "/.well-known/apple-app-site-association", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("aasa: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var aasa struct {
		Applinks struct {
			Details []struct {
				AppIDs     []string            `json:"appIDs"`
				Components []map[string]string `json:"components"`
			} `json:"details"`
		} `json:"applinks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &aasa); err != nil {
		t.Fatal(err)
	}
	d := aasa.Applinks.Details[0]
	if d.AppIDs[0] != "ABCDE12345.work.covey.coveyMobile" || len(d.Components) != 2 ||
		d.Components[0]["/"] != "/pair" || d.Components[1]["/"] != "/team/*" {
		t.Fatalf("aasa claims exactly /pair and /team/*: %+v", d)
	}

	rec = httptest.NewRecorder()
	s.handleAssetLinks(rec, httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil))
	var links []struct {
		Target struct {
			Package      string   `json:"package_name"`
			Fingerprints []string `json:"sha256_cert_fingerprints"`
		} `json:"target"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &links); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("assetlinks: %d %v", rec.Code, err)
	}
	if links[0].Target.Package != "work.covey.covey_mobile" || links[0].Target.Fingerprints[0] != "AA:BB" {
		t.Fatalf("assetlinks: %+v", links)
	}
}
