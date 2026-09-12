package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The config is what `register` leaves behind and `run` picks up. A round trip
// through both is the only thing that says the two agree — they are written
// apart, and a key that one writes and the other ignores is a setting that
// silently does nothing.
func TestConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	want := config{
		URL: "https://covey.example", Token: "geheim",
		WorkDir:      "/var/lib/covey-runner",
		Images:       []string{"covey-sandbox:test", "covey-sandbox:dev"},
		Tags:         []string{"arm64", "gpu"},
		MaxSandboxes: 3,
	}
	if err := writeConfig(path, want); err != nil {
		t.Fatal(err)
	}
	// The token is what this host acts for its organisation with, so the file
	// must not be readable by anybody else.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("the config is mode %v — the token is in it", mode)
	}

	got, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.URL != want.URL || got.Token != want.Token || got.WorkDir != want.WorkDir {
		t.Errorf("scalars did not survive: %+v", got)
	}
	if got.MaxSandboxes != want.MaxSandboxes {
		t.Errorf("max_sandboxes = %d, expected %d", got.MaxSandboxes, want.MaxSandboxes)
	}
	if strings.Join(got.Images, ",") != strings.Join(want.Images, ",") {
		t.Errorf("images = %v", got.Images)
	}
	if strings.Join(got.Tags, ",") != strings.Join(want.Tags, ",") {
		t.Errorf("tags = %v", got.Tags)
	}
}

// A hand-written config without an image claim must NOT get one through the
// default. A claim nobody made cost an organisation its data plane once: the
// registered runner claimed covey-sandbox:latest, the agents needed the deploy
// image, and nobody was a candidate.
func TestReadConfigClaimsNoImageByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("url = \"https://covey.example\"\ntoken = \"t\"\n"), 0o600)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Images) != 0 {
		t.Errorf("a config without images got %v", cfg.Images)
	}
	// The work directory does have a default — without one there is nowhere
	// to put a home.
	if cfg.WorkDir == "" {
		t.Error("work_dir has no default")
	}
}

// A typo in max_sandboxes that silently means "no limit" is the one failure
// this setting exists to prevent.
func TestReadConfigRefusesAnUnreadableLimit(t *testing.T) {
	for _, value := range []string{`"drei"`, `"-1"`, `"3.5"`} {
		path := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(path, []byte("max_sandboxes = "+value+"\n"), 0o600)
		if _, err := readConfig(path); err == nil {
			t.Errorf("max_sandboxes = %s was accepted", value)
		}
	}
	// Zero IS a value: it means no limit, and somebody wrote it down.
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("max_sandboxes = \"0\"\n"), 0o600)
	cfg, err := readConfig(path)
	if err != nil || cfg.MaxSandboxes != 0 {
		t.Errorf("max_sandboxes = 0: %+v, %v", cfg, err)
	}
}

// Without a config there is nothing to run, and the message has to say what to
// do — a runner is installed by somebody who has just met it.
func TestReadConfigWithoutAFileSaysWhatToDo(t *testing.T) {
	_, err := readConfig(filepath.Join(t.TempDir(), "gibtesnicht.toml"))
	if err == nil {
		t.Fatal("a missing config was accepted")
	}
	if !strings.Contains(err.Error(), "register") {
		t.Errorf("the message does not name the way out: %v", err)
	}
}

// Comments and blank lines are what the written file is full of, and a
// hand-edited one has more. Neither may become a setting.
func TestReadConfigIgnoresCommentsAndNoise(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`# ein Kommentar

url = "https://covey.example"
eine zeile ohne gleichheitszeichen
# token = "nicht dieser"
token = "echt"
`), 0o600)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "echt" {
		t.Errorf("token = %q — a commented-out line was read", cfg.Token)
	}
	if cfg.URL != "https://covey.example" {
		t.Errorf("url = %q", cfg.URL)
	}
}

func TestQuoteAndParseList(t *testing.T) {
	if got := quoteList(nil); got != "" {
		t.Errorf("quoteList(nil) = %q", got)
	}
	if got := quoteList([]string{"a", "b"}); got != `"a", "b"` {
		t.Errorf("quoteList = %q", got)
	}
	for _, tc := range []struct {
		in   string
		want string
	}{
		{`["a", "b"]`, "a,b"},
		{`[]`, ""},
		{``, ""},
		{`[ "a" ,, "b" ]`, "a,b"},
	} {
		if got := strings.Join(parseList(tc.in), ","); got != tc.want {
			t.Errorf("parseList(%q) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

func TestFirstOr(t *testing.T) {
	if got := firstOr(nil, "fallback"); got != "fallback" {
		t.Errorf("firstOr(nil) = %q", got)
	}
	if got := firstOr([]string{"erst", "dann"}, "fallback"); got != "erst" {
		t.Errorf("firstOr = %q", got)
	}
}

func TestStringListFlag(t *testing.T) {
	var l stringList
	l.Set("eins")
	l.Set("zwei")
	if got := l.String(); got != "eins,zwei" {
		t.Errorf("stringList = %q", got)
	}
}

// register and whoami are the two calls before the protocol takes over. Both
// have to fail with a sentence somebody can act on — a runner is set up by a
// person at a terminal, and "connection refused" is not an instruction.
func TestRegisterAndWhoami(t *testing.T) {
	ctx := context.Background()
	runnerID, orgID := uuid.New(), uuid.New()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/runner/v1/register":
			var in registerRequest
			json.NewDecoder(r.Body).Decode(&in)
			if in.Token != "gut" {
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "this registration token is used up"})
				return
			}
			if in.Version == "" || in.Arch == "" {
				t.Error("the runner did not say what it is")
			}
			json.NewEncoder(w).Encode(map[string]any{
				"runner_id": runnerID, "org_id": orgID, "token": "eigenes-token",
			})
		case "/api/runner/v1/whoami":
			if r.Header.Get("Authorization") != "Bearer eigenes-token" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"runner_id": runnerID, "org_id": orgID})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// A trailing slash on the address must not produce a double one in the path.
	id, token, err := register(ctx, srv.URL+"/", "gut", "mein Rechner", []string{"arm64"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if token != "eigenes-token" || id.RunnerID != runnerID || id.OrgID != orgID {
		t.Errorf("register gave %+v / %q", id, token)
	}

	// A refusal carries the control plane's own sentence, not just a status.
	if _, _, err := register(ctx, srv.URL, "verbraucht", "", nil); err == nil ||
		!strings.Contains(err.Error(), "used up") {
		t.Errorf("the refusal does not carry its reason: %v", err)
	}

	who, err := whoami(ctx, srv.URL, "eigenes-token")
	if err != nil {
		t.Fatalf("whoami: %v", err)
	}
	if who.RunnerID != runnerID {
		t.Errorf("whoami answered for %s", who.RunnerID)
	}
	// A wrong token says so here rather than as a WebSocket closing without a
	// reason.
	_, err = whoami(ctx, srv.URL, "falsch")
	if err == nil || !strings.Contains(err.Error(), "another instance") {
		t.Errorf("the message does not explain the 401: %v", err)
	}

	// An address nothing answers on names the address.
	if _, err := whoami(ctx, "http://127.0.0.1:1", "t"); err == nil ||
		!strings.Contains(err.Error(), "not reachable") {
		t.Errorf("an unreachable control plane: %v", err)
	}
}

func TestMessageKeepsThePartThatSaysSomething(t *testing.T) {
	if got := message(strings.NewReader(`{"error":"so nicht"}`)); got != "so nicht" {
		t.Errorf("message = %q", got)
	}
	if got := message(strings.NewReader("nur text")); got != "nur text" {
		t.Errorf("message = %q", got)
	}
}
