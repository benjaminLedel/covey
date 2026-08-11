// Package ios brokers a narrow set of Xcode/Simulator actions to a small
// helper process that runs directly on the macOS host (cmd/coveyios) — not
// in any container. Every other target system in Covey reaches OUT to an
// external SaaS from inside the disposable sandbox; this one is the
// exception, because Xcode and Simulator.app are macOS-only and cannot run
// inside a Linux container, sandboxed or emulated. The bridge does the
// actual git clone/pod install/xcodebuild/simctl work on the trusted host;
// this plugin only forwards a whitelisted request and relays the result —
// it never hands the agent a shell on the host.
package ios

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"covey/internal/target"
)

// System binds the plugin into the target registry.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:  "ios",
		Label: "iOS (Xcode/Simulator)",
		Description: "Builds the iOS host app of order-system-app and previews it in the " +
			"Simulator, on the actual Mac the control plane runs on — via a small local " +
			"bridge (cmd/coveyios), because Xcode/Simulator.app cannot run inside any Linux " +
			"container. No external account; the bridge's own token gates who may trigger it.",
		Kind:          "builtin",
		Category:      target.CategoryDev,
		System:        System{},
		NoCredentials: true,
		SetupDoc: `1. On the Mac that runs the control plane, start the bridge once:
     go run ./cmd/coveyios  (or the built binary)
   It needs Xcode command-line tools, CocoaPods ("pod") and a git identity
   with read access to the order-system-app repo already configured on
   THAT host — none of that runs in a container.

2. Store the bridge's own token (printed on its first start, or set via
   COVEY_IOS_BRIDGE_TOKEN before starting it) as the secret "ios_bridge_token"
   and assign it to the agent — actions reference it as
   {{secret:ios_bridge_token}} in their own params, the same way a registry
   login token is used, since this system carries no stored connection.

3. Enable it in the agent's ACCESS.md:
   - system: ios scope: build,simulator
   Grant "build" alone for an agent that should only get pass/fail + logs;
   add "simulator" for one that should also see a screenshot of the result.`,
	})
}

func (System) Name() string { return "ios" }

func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "ios:" + action
}

// bridgeURL is resolved once per action from the environment. Empty means
// unconfigured — every call fails clearly instead of dialing a made-up
// default that happens to be wrong for this deployment.
func bridgeURL() string {
	if v := strings.TrimSpace(os.Getenv("COVEY_IOS_BRIDGE_URL")); v != "" {
		return v
	}
	// host.docker.internal is already routed for every sandbox container
	// (sandbox_docker.go); the bridge's own default port.
	return "http://host.docker.internal:8496"
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, _ target.Credential) (any, error) {
	switch action {
	case "build":
		return doBuild(ctx, params)
	case "build_log":
		return doBuildLog(ctx, params)
	case "preview":
		return doPreview(ctx, params)
	default:
		return nil, fmt.Errorf("unknown action %q", action)
	}
}

type buildParams struct {
	Ref    string `json:"ref"`
	Scheme string `json:"scheme"`
	Test   bool   `json:"test"`
	Token  string `json:"token"`
}

type buildResult struct {
	Success  bool   `json:"success"`
	BuildID  string `json:"build_id"`
	LogTail  string `json:"log_tail"`
	Error    string `json:"error,omitempty"`
	AppPath  string `json:"-"` // internal only, never returned to the model
}

func doBuild(ctx context.Context, raw json.RawMessage) (any, error) {
	var in buildParams
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	if strings.TrimSpace(in.Ref) == "" {
		return nil, fmt.Errorf("ref missing: which branch/SHA of order-system-app to build")
	}
	var out buildResult
	if err := callBridge(ctx, in.Token, "POST", "/build", map[string]any{
		"ref": in.Ref, "scheme": in.Scheme, "test": in.Test,
	}, &out); err != nil {
		return nil, err
	}
	out.AppPath = ""
	return out, nil
}

type buildLogParams struct {
	BuildID   string `json:"build_id"`
	TailLines int    `json:"tail_lines"`
	Token     string `json:"token"`
}

func doBuildLog(ctx context.Context, raw json.RawMessage) (any, error) {
	var in buildLogParams
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	if strings.TrimSpace(in.BuildID) == "" {
		return nil, fmt.Errorf("build_id missing")
	}
	var out struct {
		Log string `json:"log"`
	}
	path := fmt.Sprintf("/build/%s/log?tail_lines=%d", in.BuildID, in.TailLines)
	if err := callBridge(ctx, in.Token, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type previewParams struct {
	BuildID string `json:"build_id"`
	Device  string `json:"device"`
	Token   string `json:"token"`
}

func doPreview(ctx context.Context, raw json.RawMessage) (any, error) {
	var in previewParams
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	if strings.TrimSpace(in.BuildID) == "" {
		return nil, fmt.Errorf("build_id missing: preview needs the build_id a prior build action returned")
	}
	var out struct {
		Success    bool   `json:"success"`
		Error      string `json:"error,omitempty"`
		Screenshot string `json:"screenshot_png_base64,omitempty"`
	}
	if err := callBridge(ctx, in.Token, "POST", "/preview", map[string]any{
		"build_id": in.BuildID, "device": in.Device,
	}, &out); err != nil {
		return nil, err
	}
	result := map[string]any{"success": out.Success}
	if out.Error != "" {
		result["error"] = out.Error
	}
	if out.Screenshot != "" {
		png, err := base64.StdEncoding.DecodeString(out.Screenshot)
		if err != nil {
			return nil, fmt.Errorf("bridge returned an unreadable screenshot: %w", err)
		}
		target.EmitArtifact(ctx, target.Artifact{MIME: "image/png", Bytes: png})
		result["note"] = "screenshot attached to this action's recording entry"
	}
	return result, nil
}

// callBridge is the one place that talks to the host bridge — every action
// goes through it so the bearer token, timeout and error shape stay
// consistent. body == nil means no request body (GET).
func callBridge(ctx context.Context, token, method, path string, body any, out any) error {
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("token missing: pass {{secret:ios_bridge_token}} as \"token\"")
	}
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute) // a real Xcode build is slow
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, bridgeURL()+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("ios bridge unreachable (is cmd/coveyios running on the host? %s): %w", bridgeURL(), err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read bridge response: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("ios bridge rejected the token — check the ios_bridge_token secret matches what the bridge was started with")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ios bridge: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("decode bridge response: %w", err)
		}
	}
	return nil
}

func (System) PromptDoc() string {
	return `Available ios actions — order-system-app's iOS host, built and previewed on the
   actual Mac the platform runs on (Xcode/Simulator.app cannot run in your own sandbox):
   build {"ref":"<branch or SHA>","scheme":"iosApp","test":false,"token":"{{secret:ios_bridge_token}}"} —
   clones order-system-app at ref on the HOST (not your sandbox — it has no local checkout to
   reuse), runs "pod install", then "xcodebuild ... -destination generic/platform=iOS Simulator
   build" (or "test":true for the test action against a real simulator destination). Returns
   {"success":bool,"build_id":"...","log_tail":"<last lines>","error":"<only on failure>"}. A
   real Xcode build is slow (minutes) — this action blocks until it finishes, do not assume a
   quick reply.
   build_log {"build_id":"...","tail_lines":300,"token":"..."} — the fuller log of a build, e.g.
   after a failure whose log_tail was not enough to see the actual error.
   preview {"build_id":"...","device":"StockiTest17Pro","token":"..."} — needs "simulator" scope.
   Installs and launches the app from a SUCCESSFUL build on the named Simulator (boots it first
   if needed) and takes a screenshot. The screenshot is attached to this action's recording
   entry, not returned inline — open the recording to actually look at it rather than expecting
   image data in the result. Only "build":true results have an app to install; a "test" build has
   none, "preview" on one fails.
   Both actions need a build/preview scheme this project actually declares — "iosApp" is the only
   one documented in iosApp/README.md; do not guess another name.`
}
