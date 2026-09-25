package httpapi

import "net/http"

// App links (#333): the two files with which iOS and Android decide that a
// link to this host belongs to the covey app, not to the browser.
//
// Only two kinds of path are claimed, and deliberately no more:
//
//   - /pair — the pairing link in the QR code (#330). The phone's own camera
//     opens the app with it, and the app confirms before it pairs.
//   - /team/* — a thread. The same address the web's team surface uses, so a
//     link somebody shares opens where the reader works.
//
// Everything else stays with the browser: claiming the whole host would send
// a person who taps a settings link into an app that has no settings.
//
// Both files depend on who ships the app, not on the installation, so they
// exist only when configured (config.AppleAppID, AndroidCertSHA256) and are an
// honest 404 otherwise.

// appLinkPaths are the paths claimed for the app, in both files' syntax.
var appLinkPaths = []string{"/pair", "/team/*"}

func (s *Server) handleAppleAppSiteAssociation(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil || s.Config.AppleAppID == "" {
		http.NotFound(w, r)
		return
	}
	components := make([]map[string]string, 0, len(appLinkPaths))
	for _, p := range appLinkPaths {
		components = append(components, map[string]string{"/": p})
	}
	// Apple fetches this through its CDN and wants JSON without a redirect
	// and without an extension; the content type is what it checks.
	writeJSON(w, http.StatusOK, map[string]any{
		"applinks": map[string]any{
			"details": []map[string]any{{
				"appIDs":     []string{s.Config.AppleAppID},
				"components": components,
			}},
		},
	})
}

func (s *Server) handleAssetLinks(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil || len(s.Config.AndroidCertSHA256) == 0 || s.Config.AndroidAppPackage == "" {
		http.NotFound(w, r)
		return
	}
	// Android's statement list claims the whole host; which paths open the
	// app is said by the intent filters in the app's manifest, and those name
	// the same two as appLinkPaths.
	writeJSON(w, http.StatusOK, []map[string]any{{
		"relation": []string{"delegate_permission/common.handle_all_urls"},
		"target": map[string]any{
			"namespace":                "android_app",
			"package_name":             s.Config.AndroidAppPackage,
			"sha256_cert_fingerprints": s.Config.AndroidCertSHA256,
		},
	}})
}
