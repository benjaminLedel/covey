package sandbox

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Reading a project's `docker-compose.yml` — the file that already answers
// "what does this project need in order to run".
//
// It is READ here, never executed. The difference is the whole point: compose
// on a developer's machine may publish ports, mount host directories and build
// images, because that machine is theirs. On a runner it is somebody else's,
// and those three are exactly the keys that would make a service a way onto it.
//
// So the parse produces two lists rather than one verdict. What can run, and
// what cannot — with the reason, per service. An all-or-nothing error would be
// the wrong shape: nearly every compose file contains a service that must not
// run here (the project's own application, which the agent builds inside its
// sandbox), and refusing the file because of it would make the mechanism
// useless for the files it exists for.

// ComposeSkip is one service that will not run, and why. The reason is written
// for the agent that will read it back — it has to be able to tell "this one is
// not for me" from "this one is not allowed".
type ComposeSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// ComposeResult is what a compose file yields.
type ComposeResult struct {
	Services []Service     `json:"services"`
	Skipped  []ComposeSkip `json:"skipped,omitempty"`
}

// composeFile is the subset that is read. Everything not named here is ignored
// by definition — a key this struct does not have cannot influence a start.
type composeFile struct {
	Services map[string]composeService `yaml:"services"`
}

type composeService struct {
	Image       string    `yaml:"image"`
	Build       yaml.Node `yaml:"build"`
	Environment yaml.Node `yaml:"environment"`
	Volumes     []string  `yaml:"volumes"`
	Privileged  bool      `yaml:"privileged"`
	NetworkMode string    `yaml:"network_mode"`
	CapAdd      []string  `yaml:"cap_add"`
	Devices     []string  `yaml:"devices"`
	Pid         string    `yaml:"pid"`
}

// ParseCompose reads the subset of a compose file that may run beside a
// sandbox. It does not check the allowlist — that is the organisation's
// question and is asked one layer up, where the organisation is known.
func ParseCompose(raw []byte) (ComposeResult, error) {
	var f composeFile
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return ComposeResult{}, fmt.Errorf("this is not a readable compose file: %w", err)
	}
	if len(f.Services) == 0 {
		return ComposeResult{}, fmt.Errorf("the file names no services")
	}

	// Map iteration order is random, and a list of services that comes out in a
	// different order every time is one nobody can diff or test against.
	names := make([]string, 0, len(f.Services))
	for name := range f.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	var out ComposeResult
	for _, name := range names {
		svc := f.Services[name]
		if reason := composeRefusal(svc); reason != "" {
			out.Skipped = append(out.Skipped, ComposeSkip{Name: name, Reason: reason})
			continue
		}
		out.Services = append(out.Services, Service{
			Name:  strings.ToLower(strings.TrimSpace(name)),
			Image: strings.TrimSpace(svc.Image),
			Env:   composeEnv(svc.Environment),
		})
	}
	// The names have to survive being host names and container names; the same
	// rules as for a typed declaration, and the same place decides them.
	valid, err := ValidateServices(out.Services)
	if err != nil {
		return ComposeResult{}, err
	}
	out.Services = valid
	return out, nil
}

// composeRefusal names why a service cannot run beside a sandbox — empty means
// it can. Each of these is a key that would either not work here or would make
// the service a way onto the host.
func composeRefusal(svc composeService) string {
	if !svc.Build.IsZero() {
		// Not a refusal in the security sense, and the reason it is first: this
		// is nearly always the project's OWN application, and that belongs
		// inside the sandbox where the agent builds and runs it. A copy of it
		// standing beside the sandbox would be a second, older instance of the
		// thing under test.
		return "it is built from the project itself — that one belongs in your sandbox, where you build it, not beside it"
	}
	if strings.TrimSpace(svc.Image) == "" {
		return "it names no image"
	}
	for _, v := range svc.Volumes {
		// A named volume (`data:/var/lib/postgresql`) is harmless; a host path
		// is a hole into the runner. The difference is the leading dot or slash.
		if strings.HasPrefix(v, "/") || strings.HasPrefix(v, ".") || strings.HasPrefix(v, "~") {
			return "it mounts a directory from the host (" + v + ") — on a runner that is somebody else's machine"
		}
	}
	if svc.Privileged {
		return "it asks for `privileged`, which would take the isolation away"
	}
	if len(svc.CapAdd) > 0 {
		return "it asks for extra capabilities (cap_add)"
	}
	if len(svc.Devices) > 0 {
		return "it asks for host devices"
	}
	if m := strings.TrimSpace(svc.NetworkMode); m != "" && m != "bridge" {
		return "it asks for the network mode " + m + " — the services here share a network of the sandbox's own"
	}
	if p := strings.TrimSpace(svc.Pid); p != "" {
		return "it asks for the pid namespace " + p
	}
	return ""
}

// composeEnv reads both spellings compose allows: a mapping, and a list of
// KEY=VALUE strings. Both are common in the wild, and a parser that knew only
// one would fail on half the files for a reason nobody would guess.
func composeEnv(node yaml.Node) map[string]string {
	out := map[string]string{}
	switch node.Kind {
	case yaml.MappingNode:
		var m map[string]string
		if err := node.Decode(&m); err != nil {
			return nil
		}
		for k, v := range m {
			out[strings.TrimSpace(k)] = v
		}
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return nil
		}
		for _, entry := range list {
			k, v, found := strings.Cut(entry, "=")
			if !found {
				// `- FOO` means "pass FOO through from the environment". There
				// is no environment to pass through from here, and inventing an
				// empty value would be worse than leaving it out: the service
				// then fails saying the variable is missing, which is true.
				continue
			}
			out[strings.TrimSpace(k)] = v
		}
	default:
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
