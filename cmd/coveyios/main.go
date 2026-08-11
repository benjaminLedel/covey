// coveyios is a small HTTP bridge for exactly one purpose: giving Covey
// agents a way to build order-system-app's iOS host and preview it in the
// Simulator. It exists because Xcode and Simulator.app are macOS-only —
// no sandbox, container or emulation layer in Covey's normal data plane can
// run them. So unlike every other target system, this one does NOT run
// inside an agent's disposable sandbox: it runs directly on the trusted
// macOS host, started deliberately by the operator, and only ever performs
// the small, fixed set of operations below — never an arbitrary command a
// caller supplies.
//
// Configuration exclusively via ENV, same convention as coveyd:
//
//	COVEY_IOS_BRIDGE_ADDR      listen address (default ":8496")
//	COVEY_IOS_BRIDGE_TOKEN     bearer token clients must present (generated
//	                           and persisted under Workdir on first start if unset)
//	COVEY_IOS_BRIDGE_REPO      git URL of the ONE repo this bridge will clone
//	                           (default order-system-app on gitlab.com)
//	COVEY_IOS_BRIDGE_WORKDIR   scratch checkouts + build outputs
//	                           (default ~/.covey-ios-bridge)
//	COVEY_IOS_BRIDGE_SCHEMES   comma-separated scheme allowlist (default "iosApp")
//	COVEY_IOS_BRIDGE_DEVICE    default Simulator device name for preview
//	                           (default "StockiTest17Pro")
//	COVEY_IOS_BRIDGE_BUNDLE_ID app bundle id to launch on preview
//	                           (default "de.softwarebees.stocki")
package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type config struct {
	Addr     string
	Token    string
	RepoURL  string
	Workdir  string
	Schemes  []string
	Device   string
	BundleID string
}

func loadConfig() (config, error) {
	home, _ := os.UserHomeDir()
	cfg := config{
		Addr:     env("COVEY_IOS_BRIDGE_ADDR", ":8496"),
		RepoURL:  env("COVEY_IOS_BRIDGE_REPO", "https://gitlab.com/softwarebees/order-system/order-system-app.git"),
		Workdir:  env("COVEY_IOS_BRIDGE_WORKDIR", filepath.Join(home, ".covey-ios-bridge")),
		Schemes:  splitCSV(env("COVEY_IOS_BRIDGE_SCHEMES", "iosApp")),
		Device:   env("COVEY_IOS_BRIDGE_DEVICE", "StockiTest17Pro"),
		BundleID: env("COVEY_IOS_BRIDGE_BUNDLE_ID", "de.softwarebees.stocki"),
	}
	if err := os.MkdirAll(cfg.Workdir, 0o755); err != nil {
		return cfg, fmt.Errorf("workdir: %w", err)
	}
	token, err := resolveToken(cfg.Workdir)
	if err != nil {
		return cfg, err
	}
	cfg.Token = token
	return cfg, nil
}

// resolveToken: an explicit COVEY_IOS_BRIDGE_TOKEN always wins. Otherwise a
// token is generated once and persisted under Workdir — restarting the
// bridge without the env var must not invalidate the secret already stored
// in Covey (ios_bridge_token), the same reasoning as .covey.key for
// COVEY_MASTER_KEY.
func resolveToken(workdir string) (string, error) {
	if v := strings.TrimSpace(os.Getenv("COVEY_IOS_BRIDGE_TOKEN")); v != "" {
		return v, nil
	}
	path := filepath.Join(workdir, "token")
	if b, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			return t, nil
		}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("persist token: %w", err)
	}
	return token, nil
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

type bridge struct {
	cfg config
	log *slog.Logger

	mu     sync.Mutex // one build at a time — xcodebuild/simctl share this Mac
	builds map[string]*buildRecord
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, err := loadConfig()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	b := &bridge{cfg: cfg, log: log, builds: map[string]*buildRecord{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", b.handleHealth)
	mux.HandleFunc("POST /build", b.withAuth(b.handleBuild))
	mux.HandleFunc("GET /build/{id}/log", b.withAuth(b.handleBuildLog))
	mux.HandleFunc("POST /preview", b.withAuth(b.handlePreview))

	log.Info("coveyios bridge starting",
		"addr", cfg.Addr, "repo", cfg.RepoURL, "schemes", cfg.Schemes, "device", cfg.Device)
	log.Warn("this process runs XCODE BUILDS AND SIMULATOR ACTIONS ON THIS MAC on behalf of " +
		"whichever Covey agent holds the ios_bridge_token secret — treat that secret like a " +
		"credential, because on this host it effectively is one")
	fmt.Fprintf(os.Stderr, "\nios_bridge_token (store this as the Covey secret \"ios_bridge_token\"):\n  %s\n\n", cfg.Token)

	if err := http.ListenAndServe(cfg.Addr, mux); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

func (b *bridge) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(b.cfg.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (b *bridge) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"xcodebuild": toolVersion("xcodebuild", "-version"),
		"pod":        toolVersion("pod", "--version"),
		"git":        toolVersion("git", "--version"),
	})
}

func (b *bridge) handleBuild(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ref    string `json:"ref"`
		Scheme string `json:"scheme"`
		Test   bool   `json:"test"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	if err := validateRef(in.Ref); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scheme := in.Scheme
	if scheme == "" {
		scheme = b.cfg.Schemes[0]
	}
	if !contains(b.cfg.Schemes, scheme) {
		http.Error(w, fmt.Sprintf("scheme %q is not in the allowlist %v", scheme, b.cfg.Schemes), http.StatusBadRequest)
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	rec := b.runBuild(r.Context(), in.Ref, scheme, in.Test)
	b.builds[rec.ID] = rec
	writeJSON(w, http.StatusOK, map[string]any{
		"success":  rec.Success,
		"build_id": rec.ID,
		"log_tail": tail(rec.Log, 4000),
		"error":    rec.Error,
	})
}

func (b *bridge) handleBuildLog(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	b.mu.Lock()
	rec, ok := b.builds[id]
	b.mu.Unlock()
	if !ok {
		http.Error(w, "unknown build_id", http.StatusNotFound)
		return
	}
	n := 2000
	if v := r.URL.Query().Get("tail_lines"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"log": tailLines(rec.Log, n)})
}

func (b *bridge) handlePreview(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BuildID string `json:"build_id"`
		Device  string `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	device := in.Device
	if device == "" {
		device = b.cfg.Device
	}
	if err := validateDevice(device); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	b.mu.Lock()
	rec, ok := b.builds[in.BuildID]
	b.mu.Unlock()
	if !ok {
		http.Error(w, "unknown build_id", http.StatusNotFound)
		return
	}
	if !rec.Success || rec.AppPath == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   "build has no installable app (it failed, or was a \"test\" build)",
		})
		return
	}

	png, err := b.runPreview(r.Context(), device, rec.AppPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"success": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"success":               true,
		"screenshot_png_base64": encodeBase64(png),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
