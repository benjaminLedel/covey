package sandbox

import "testing"

// A compose file of the shape that actually turns up in a repository: an
// application built from the project itself, and the services it talks to.
const realistic = `
services:
  app:
    build: .
    ports: ["8080:8080"]
    depends_on: [db, cache]
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: secret
      POSTGRES_DB: app
    volumes:
      - dbdata:/var/lib/postgresql/data
  cache:
    image: redis:7
    environment:
      - REDIS_ARGS=--save 60 1
      - PASSTHROUGH
volumes:
  dbdata:
`

func TestParseComposeTakesTheServicesAndLeavesTheApplication(t *testing.T) {
	got, err := ParseCompose([]byte(realistic))
	if err != nil {
		t.Fatalf("ParseCompose: %v", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("got %d services, want 2: %+v", len(got.Services), got.Services)
	}
	// Sorted, because a list that comes out in a different order every time is
	// one nobody can diff.
	if got.Services[0].Name != "cache" || got.Services[1].Name != "db" {
		t.Errorf("not in a stable order: %+v", got.Services)
	}
	if got.Services[1].Image != "postgres:16" || got.Services[1].Env["POSTGRES_PASSWORD"] != "secret" {
		t.Errorf("the mapping form of environment was not read: %+v", got.Services[1])
	}
	// The list form is as common as the mapping form.
	if got.Services[0].Env["REDIS_ARGS"] != "--save 60 1" {
		t.Errorf("the list form of environment was not read: %+v", got.Services[0])
	}
	// `- PASSTHROUGH` means "take it from the environment". There is none here,
	// and an invented empty value would be worse than the service saying the
	// variable is missing.
	if _, ok := got.Services[0].Env["PASSTHROUGH"]; ok {
		t.Errorf("a pass-through variable was invented: %+v", got.Services[0].Env)
	}
	// The application is skipped, with a reason an agent can act on — a named
	// volume is not a reason for anything.
	if len(got.Skipped) != 1 || got.Skipped[0].Name != "app" {
		t.Fatalf("the application was not skipped: %+v", got.Skipped)
	}
	if got.Skipped[0].Reason == "" {
		t.Error("skipped without a reason")
	}
}

// The three keys that would make a service a way onto the runner.
func TestParseComposeRefusesWhatWouldOpenTheHost(t *testing.T) {
	cases := map[string]string{
		"a host bind mount":  "services:\n  x:\n    image: postgres:16\n    volumes: ['/etc:/etc']\n",
		"a relative mount":   "services:\n  x:\n    image: postgres:16\n    volumes: ['./data:/data']\n",
		"privileged":         "services:\n  x:\n    image: postgres:16\n    privileged: true\n",
		"extra capabilities": "services:\n  x:\n    image: postgres:16\n    cap_add: [SYS_ADMIN]\n",
		"a host device":      "services:\n  x:\n    image: postgres:16\n    devices: ['/dev/kvm']\n",
		"the host network":   "services:\n  x:\n    image: postgres:16\n    network_mode: host\n",
		"the host pid space": "services:\n  x:\n    image: postgres:16\n    pid: host\n",
	}
	for what, file := range cases {
		got, err := ParseCompose([]byte(file))
		if err != nil {
			t.Errorf("%s: %v", what, err)
			continue
		}
		if len(got.Services) != 0 {
			t.Errorf("%s was allowed to run: %+v", what, got.Services)
		}
		if len(got.Skipped) != 1 || got.Skipped[0].Reason == "" {
			t.Errorf("%s was skipped without a reason: %+v", what, got.Skipped)
		}
	}
}

// A named volume is the normal way a compose file keeps a database's data and
// says nothing about the host — it must not cost the service its place.
func TestParseComposeKeepsAServiceWithANamedVolume(t *testing.T) {
	got, err := ParseCompose([]byte("services:\n  db:\n    image: postgres:16\n    volumes: ['dbdata:/var/lib/postgresql/data']\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("the named volume cost the service its place: %+v", got)
	}
}

func TestParseComposeRefusesWhatItCannotRead(t *testing.T) {
	for what, file := range map[string]string{
		"not yaml at all":                "\t\tthis: [is: not",
		"no services":                    "version: '3'\n",
		"a name that is not a host name": "services:\n  'My DB':\n    image: postgres:16\n",
	} {
		if _, err := ParseCompose([]byte(file)); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
}
