package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type buildRecord struct {
	ID      string
	Success bool
	Error   string
	Log     string
	AppPath string // empty for a "test" build, or on failure
}

// refPattern and devicePattern are deliberately strict: both values end up as
// argv entries to git/xcodebuild/simctl via exec.Command, which never invokes
// a shell — so there is no shell-injection surface — but an argument that
// starts with "-" could still be misread as a flag by the tool itself
// (git checkout --upload-pack=... style argument injection). Rejecting
// anything outside a plain ref/device-name shape closes that off without
// needing to reason about every tool's own flag parsing.
var (
	refPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	devicePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,99}$`)
)

func validateRef(ref string) error {
	if !refPattern.MatchString(ref) {
		return fmt.Errorf("ref %q does not look like a git branch/SHA (allowed: letters, digits, dot, underscore, dash, slash)", ref)
	}
	return nil
}

func validateDevice(device string) error {
	if !devicePattern.MatchString(device) {
		return fmt.Errorf("device %q does not look like a Simulator device name", device)
	}
	return nil
}

func newBuildID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

// runBuild clones the ONE configured repo (never a caller-supplied URL) at
// ref, runs "pod install", then xcodebuild. Every step's combined
// stdout+stderr is appended to the same log so a failure at any stage is
// visible in one place. The mutex in bridge.handleBuild already keeps builds
// from overlapping — this Mac's Simulator and DerivedData are shared state.
func (b *bridge) runBuild(ctx context.Context, ref, scheme string, test bool) *buildRecord {
	rec := &buildRecord{ID: newBuildID()}
	dir := filepath.Join(b.cfg.Workdir, "builds", rec.ID)
	srcDir := filepath.Join(dir, "src")
	var log bytes.Buffer

	run := func(name string, workdir string, args ...string) bool {
		fmt.Fprintf(&log, "\n$ %s %s   (in %s)\n", name, strings.Join(args, " "), workdir)
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Dir = workdir
		cmd.Stdout = &log
		cmd.Stderr = &log
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(&log, "\n! %s failed: %v\n", name, err)
			rec.Error = fmt.Sprintf("%s: %v", name, err)
			return false
		}
		return true
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		rec.Error = fmt.Sprintf("workdir: %v", err)
		rec.Log = log.String()
		return rec
	}

	switch {
	case !run("git", dir, "clone", b.cfg.RepoURL, srcDir):
	case !run("git", srcDir, "checkout", ref):
	case !run("pod", filepath.Join(srcDir, "iosApp"), "install", "--repo-update"):
	default:
		derivedData := filepath.Join(dir, "DerivedData")
		destination := "generic/platform=iOS Simulator"
		action := "build"
		if test {
			action = "test"
			destination = "platform=iOS Simulator,name=" + b.cfg.Device
		}
		podDir := filepath.Join(srcDir, "iosApp")
		if run("xcodebuild", podDir,
			"-workspace", "iosApp.xcworkspace",
			"-scheme", scheme,
			"-destination", destination,
			"-derivedDataPath", derivedData,
			action) {
			rec.Success = true
			if !test {
				appPath := filepath.Join(derivedData, "Build", "Products", "Debug-iphonesimulator", scheme+".app")
				if _, err := os.Stat(appPath); err == nil {
					rec.AppPath = appPath
				} else {
					fmt.Fprintf(&log, "\n! build succeeded but the expected app bundle is missing at %s: %v\n", appPath, err)
					rec.Success = false
					rec.Error = "build reported success but produced no .app at the expected path — see the log"
				}
			}
		}
	}

	rec.Log = log.String()
	return rec
}

// runPreview boots the device if needed, installs and launches the app from
// a successful build, then takes a screenshot. "already booted" from simctl
// boot is the expected case on a Mac that already has this device running —
// only install/launch failures are treated as real errors.
func (b *bridge) runPreview(ctx context.Context, device, appPath string) ([]byte, error) {
	_ = exec.CommandContext(ctx, "xcrun", "simctl", "boot", device).Run() // ignore: usually "already booted"

	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "install", device, appPath).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("simctl install: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "launch", device, b.cfg.BundleID).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("simctl launch: %v: %s", err, strings.TrimSpace(string(out)))
	}
	time.Sleep(2 * time.Second) // let the UI actually render before the screenshot

	shot := filepath.Join(os.TempDir(), "coveyios-"+newBuildID()+".png")
	defer os.Remove(shot)
	if out, err := exec.CommandContext(ctx, "xcrun", "simctl", "io", device, "screenshot", shot).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("simctl screenshot: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return os.ReadFile(shot)
}

func toolVersion(name string, args ...string) string {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "unavailable: " + err.Error()
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

func tail(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	return "… " + s[len(s)-maxBytes:]
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

func encodeBase64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}
