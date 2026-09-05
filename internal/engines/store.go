package engines

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The store materialises catalogue entries on the machine that starts sandboxes.
//
// It sits on the RUNNER side, not in the sandbox, and that placement is the
// design (spec/26): an engine is the same for every agent on a host, so it is
// fetched once per host and mounted read-only into each sandbox that names it.
// The alternative — one sandbox image per engine — is the multiplication this
// catalogue exists to end, and the agent home, where the download would happen
// once per agent and add to the walk every wake already pays.
//
// What lands here is code that runs inside other people's sandboxes, so three
// properties are structural rather than requested:
//
//   - a layer is written to a temporary directory and renamed into place, and
//     the marker file that declares it complete is written last. A directory
//     without a marker is a crashed install, not an engine;
//   - an artefact is used only after its digest matched what the catalogue
//     promised (§ Digest in spec/26);
//   - a tarball is unpacked with the traversal guard every unpacker needs —
//     these bytes come off a network and into a directory the runner mounts into
//     a container.
type Store struct {
	// Dir is the store root, typically <DataDir>/engines.
	Dir string
	// HTTP for the tarball fetch; nil = a client with a generous timeout (an
	// engine archive is hundreds of megabytes, not a document).
	HTTP *http.Client
	Log  *slog.Logger
	// Npm is the npm binary, overridable for tests and for an installation that
	// keeps node somewhere the PATH does not reach.
	Npm string
	// MaxArtifact caps a download. An engine is not a few megabytes but it is
	// bounded, and an unbounded read from a foreign host is a memory story
	// somebody else writes in their incident report.
	MaxArtifact int64
}

// maxEngineArtifact is the default cap: the largest published agent runtime seen
// is a little under half a gigabyte, and anything past that is not an engine.
const maxEngineArtifact int64 = 1 << 30

// markerFile names the file that makes a directory an installed layer.
const markerFile = ".covey-engine.json"

// LayerRoot is where one engine version lives.
func (s *Store) LayerRoot(engine, version string) string {
	return filepath.Join(s.Dir, safeSegment(engine), safeSegment(version))
}

// Layer is an installed engine, with what a run needs to know about it.
type Layer struct {
	Engine  string
	Version string
	Root    string
	Exec    string
	// RelExec is the executable relative to the layer root, slash-separated —
	// what makes the container path derivable on a host that never saw the
	// tarball. See env.go.
	RelExec     string
	Kind        string
	InstalledAt time.Time
	Release     Release
}

// marker is the layer's own record, written last and read first.
type marker struct {
	Engine      string    `json:"engine"`
	Version     string    `json:"version"`
	Kind        string    `json:"kind"`
	Integrity   string    `json:"integrity,omitempty"`
	Executable  string    `json:"executable"`
	InstalledAt time.Time `json:"installed_at"`
}

// Lookup returns an installed layer without going to the network. This is the
// path a start takes when the engine is already there — the common case, and it
// must not depend on a catalogue host being reachable.
func (s *Store) Lookup(engine, version string) (Layer, bool) {
	root := s.LayerRoot(engine, version)
	m, err := readMarker(filepath.Join(root, markerFile))
	if err != nil || m.Executable == "" {
		return Layer{}, false
	}
	ex := filepath.Join(root, filepath.FromSlash(m.Executable))
	if st, err := os.Stat(ex); err != nil || st.IsDir() {
		// Marker says one thing, the file system says another: treat it as not
		// installed rather than as installed-and-broken. Ensure will redo it.
		return Layer{}, false
	}
	return Layer{Engine: engine, Version: version, Root: root, Exec: ex, RelExec: m.Executable,
		Kind: m.Kind, InstalledAt: m.InstalledAt}, true
}

// Ensure installs the release if it is not standing there already, and returns
// the layer. The returned Exec is a path ON THIS MACHINE; the caller decides
// what the container sees, see ContainerEnv.
func (s *Store) Ensure(ctx context.Context, r Release) (Layer, error) {
	if l, ok := s.Lookup(r.engine, r.Version); ok {
		l.Release = r
		return l, nil
	}
	if err := r.Valid(); err != nil {
		return Layer{}, err
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return Layer{}, fmt.Errorf("engines: store directory: %w", err)
	}
	root := s.LayerRoot(r.engine, r.Version)
	tmp, err := os.MkdirTemp(s.Dir, safeSegment(r.engine)+"-"+safeSegment(r.Version)+".tmp")
	if err != nil {
		return Layer{}, fmt.Errorf("engines: temp layer: %w", err)
	}
	// Whatever happens below, the temporary directory does not survive it: a
	// failed install leaves no half an engine for the next start to trip over.
	defer os.RemoveAll(tmp)

	var exe string
	switch r.Kind {
	case KindTarball:
		exe, err = s.installTarball(ctx, r, tmp)
	case KindNpm:
		exe, err = s.installNpm(ctx, r, tmp)
	default:
		err = fmt.Errorf("engines: unknown kind %q", r.Kind)
	}
	if err != nil {
		return Layer{}, err
	}

	m := marker{Engine: r.engine, Version: r.Version, Kind: r.Kind,
		Integrity: r.Integrity, Executable: exe, InstalledAt: time.Now().UTC()}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return Layer{}, err
	}
	if err := os.WriteFile(filepath.Join(tmp, markerFile), body, 0o644); err != nil {
		return Layer{}, fmt.Errorf("engines: marker: %w", err)
	}
	// A previous version of this layer may stand at the target. Rename over it
	// atomically per platform: remove first, then rename — an interrupted
	// install therefore leaves either the old layer with its marker or nothing,
	// never something in between.
	// The engine's own directory first — on a fresh host it is not there yet,
	// and a rename into a missing parent is an error rather than a mkdir.
	if err := os.MkdirAll(filepath.Dir(root), 0o755); err != nil {
		return Layer{}, fmt.Errorf("engines: layer directory: %w", err)
	}
	if err := os.RemoveAll(root); err != nil {
		return Layer{}, fmt.Errorf("engines: retire the old layer: %w", err)
	}
	if err := os.Rename(tmp, root); err != nil {
		return Layer{}, fmt.Errorf("engines: install layer: %w", err)
	}
	ex := filepath.Join(root, filepath.FromSlash(exe))
	if s.Log != nil {
		s.Log.Info("engine layer installed", "engine", r.engine, "version", r.Version,
			"kind", r.Kind, "path", ex)
	}
	return Layer{Engine: r.engine, Version: r.Version, Root: root, Exec: ex, RelExec: filepath.ToSlash(exe),
		Kind: r.Kind, InstalledAt: m.InstalledAt, Release: r}, nil
}

// installTarball fetches, verifies and unpacks a tar archive (.tar or .tgz).
func (s *Store) installTarball(ctx context.Context, r Release, dst string) (string, error) {
	body, err := fetchArtifact(ctx, s.HTTP, r.URL, s.cap())
	if err != nil {
		return "", err
	}
	if err := Verify(body, r.Integrity); err != nil {
		return "", fmt.Errorf("engines: %s %s: %w", r.engine, r.Version, err)
	}
	if err := unpack(body, dst); err != nil {
		return "", fmt.Errorf("engines: %s %s: %w", r.engine, r.Version, err)
	}
	return r.innerExecutable(), nil
}

// installNpm installs one exact version into a prefix of its own.
//
// Two things here are not optional and are not defaults anyone should want to
// change: the version is spelled out (an npm range would let the registry hand
// over a different build tomorrow than today, which is the whole property a
// catalogue is for), and lifecycle scripts stay disabled unless the entry asks
// for them — see Release.AllowScripts.
func (s *Store) installNpm(ctx context.Context, r Release, dst string) (string, error) {
	npm := s.Npm
	if npm == "" {
		npm = "npm"
	}
	if _, err := exec.LookPath(npm); err != nil {
		return "", fmt.Errorf("engines: %s %s needs npm on this host (requires %s): %w",
			r.engine, r.Version, strings.Join(r.Requires, ", "), err)
	}
	target := r.Package + "@" + r.Version
	args := []string{"install", "--global=false", "--no-save", "--no-audit", "--no-fund",
		"--no-update-on-package-install", "--prefix", dst, target}
	if !r.AllowScripts {
		args = append(args, "--ignore-scripts")
	}
	if r.Registry != "" {
		args = append(args, "--registry", r.Registry)
	}
	cmd := exec.CommandContext(ctx, npm, args...)
	// npm wants a cache directory and a HOME. The runner is often started by a
	// service manager with neither writable, and the user cache is not where a
	// runner's downloads belong.
	cmd.Env = append(os.Environ(),
		"npm_config_cache="+filepath.Join(s.Dir, "npm-cache"),
		"HOME="+s.Dir, "NO_UPDATE_NOTIFIER=1", "npm_config_update_notifier=false")
	out, err := cmd.CombinedOutput()
	if err != nil {
		tail := strings.TrimSpace(string(out))
		if len(tail) > 2000 {
			tail = tail[len(tail)-2000:]
		}
		return "", fmt.Errorf("engines: npm install %s: %w — %s", target, err, tail)
	}
	// The version npm actually put down, read back rather than believed: a
	// registry that answers a pinned request with something else must be caught
	// here and not six months later in a run that behaved oddly.
	pj := filepath.Join(dst, "lib", "node_modules", r.Package, "package.json")
	have, err := packageVersion(pj)
	if err != nil {
		return "", fmt.Errorf("engines: %s %s: %w", r.engine, r.Version, err)
	}
	if have != r.Version {
		return "", fmt.Errorf("engines: %s: the registry answered %q with version %q, the catalogue pins %q",
			r.engine, r.Package, have, r.Version)
	}
	return r.innerExecutable(), nil
}

func (s *Store) cap() int64 {
	if s.MaxArtifact > 0 {
		return s.MaxArtifact
	}
	return maxEngineArtifact
}

func readMarker(path string) (*marker, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m marker
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func packageVersion(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("the package was not installed where npm puts it (%s): %w", path, err)
	}
	var doc struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("%s: %w", path, err)
	}
	if doc.Version == "" {
		return "", fmt.Errorf("%s carries no version", path)
	}
	return doc.Version, nil
}

// innerExecutable is Release.Executable with the layer root still to be joined:
// the marker stores the path RELATIVE to the layer, so a store that moves
// between machines still says where its own binary is.
func (r Release) innerExecutable() string {
	return strings.TrimPrefix(strings.TrimPrefix(r.Executable(""), "/"), "/")
}

// safeSegment keeps an engine name and a version usable as a directory name.
// Catalogue content is not hostile by assumption, but it does come from a
// network document and lands in a path the runner builds.
func safeSegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "_"
	}
	return out
}

// unpack lays a tar archive (optionally gzip-compressed) into dst. Every entry
// that would land outside dst is refused — this is the guard every untar needs
// and the reason the archive is read here rather than handed to a shell.
func unpack(body []byte, dst string) error {
	read := io.Reader(bytes.NewReader(body))
	gz, gzErr := gzip.NewReader(read)
	if gzErr == nil {
		defer gz.Close()
		read = gz
	}
	tr := tar.NewReader(read)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("not a readable tar archive: %w", err)
		}
		name := filepath.Clean(h.Name)
		if name == "." || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) ||
			filepath.IsAbs(name) || strings.Contains(h.Name, "://") {
			return fmt.Errorf("archive entry %q would land outside the layer", h.Name)
		}
		target := filepath.Join(dst, name)
		if !strings.HasPrefix(target, dst+string(filepath.Separator)) {
			return fmt.Errorf("archive entry %q would land outside the layer", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			mode := os.FileMode(h.Mode & 0o777)
			if mode == 0 {
				mode = 0o644
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			// A link whose target escapes the layer would be a way out written
			// into the very directory that gets mounted into a sandbox.
			if filepath.IsAbs(h.Linkname) || strings.Contains(h.Linkname, "..") {
				return fmt.Errorf("archive entry %q links out of the layer", h.Name)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		default:
			// Devices, FIFOs, hard links: nothing an agent runtime ships needs,
			// and each one a way to reach further than the layer.
			return fmt.Errorf("archive entry %q is of an unsupported type", h.Name)
		}
	}
}
