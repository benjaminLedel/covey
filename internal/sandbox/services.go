package sandbox

import (
	"fmt"
	"regexp"
	"strings"
)

// A workplace is an image plus the services that belong beside it.
//
// The image half was settled first (profiles.go, catalog.go): pinned by digest,
// published per Covey version. Everything a project needs BEYOND the image had
// no place — so it ended up in one of two spots that both cost. Either it was
// built into the image and operated by hand, which is what the `dev-full` and
// `dev-php` workplaces did with MariaDB: the agent was told to run
// `mariadb-install-db` into its own home, in a directory that is walked at
// every wake and written back after every run. Or it was simply missing, and
// the QA agent's procedure had one exit for it — write a finding and hand over.
//
// A service is the third thing: a container that runs BESIDE the sandbox, for
// as long as the sandbox does, reachable under its name. The agent does not
// operate it, does not install it and cannot see the host it runs on. It
// connects to `postgres:5432` and that is the whole of its knowledge.
//
// What is deliberately NOT here: no ports published to the host (the sandbox
// reaches a service over the network they share, and a published port would be
// a hole in the host nobody asked for), no volumes, no `build:`. Those are the
// parts of a compose file that only make sense on a developer's own machine —
// on a runner they are somebody else's machine.
type Service struct {
	// Name is what the sandbox reaches this service under. It becomes a DNS
	// name on the sandbox's network, so the same rules apply as to a hostname —
	// and it is the name a compose file would use, which is the point: `db`
	// stays `db`.
	Name string `json:"name"`
	// Image is the reference, as a registry states it (`postgres:16`). It is
	// NOT resolved through the workplace catalogue: that catalogue answers
	// "which image belongs to this Covey version", and a project's database is
	// not a part of Covey.
	Image string `json:"image"`
	// Env is what the image is configured with — POSTGRES_PASSWORD and its
	// like. This is a place for fixtures, not for credentials: it stands in the
	// agent's configuration, which is readable by whoever may read the agent,
	// and a service that needs a real secret is asking the broker's question
	// (spec/04), not this one.
	Env map[string]string `json:"env,omitempty"`
}

// MaxServices caps what one sandbox may bring up. The number is not a technical
// limit; it is the point past which a declaration has stopped describing a test
// environment and started describing a deployment — and a runner shared by an
// organisation should not lose its memory to one agent's idea of a stack.
const MaxServices = 10

// serviceName: a hostname, and it has to survive being part of a container
// name. Leading letter, because a name starting with a digit is ambiguous to
// resolvers that read it as an address.
var serviceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,29}$`)

// envKey is what a shell will actually pass on. Rejected here rather than
// silently dropped by docker.
var envKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedServiceNames are names the sandbox already resolves to something
// else. A service that took one of them would not shadow the original — docker
// answers from its own DNS first — but it would leave the agent with a name
// that means two things, and the second one silently.
var reservedServiceNames = map[string]bool{
	"localhost": true,
	"covey":     true,
	// The egress proxy's alias on the internal network (docker.go).
	"covey-egress": true,
}

// ValidateServices checks a declaration and returns it normalised. It is the
// one place that decides what a service may be — the API calls it before
// storing, and the runner is entitled to assume it ran.
//
// The errors are written for whoever typed the declaration, not for a log: each
// one names the service it is about, because a list of eight is where this gets
// read.
func ValidateServices(in []Service) ([]Service, error) {
	if len(in) > MaxServices {
		return nil, fmt.Errorf("%d services — at most %d may run beside one sandbox", len(in), MaxServices)
	}
	out := make([]Service, 0, len(in))
	seen := map[string]bool{}
	for _, s := range in {
		name := strings.ToLower(strings.TrimSpace(s.Name))
		if !serviceName.MatchString(name) {
			return nil, fmt.Errorf("service name %q: lower-case letters, digits and hyphens, starting with a letter, at most 30 characters — it becomes a host name", s.Name)
		}
		if reservedServiceNames[name] {
			return nil, fmt.Errorf("service name %q is taken: the sandbox already resolves it to something else", name)
		}
		if seen[name] {
			return nil, fmt.Errorf("service %q is declared twice — one name, one address", name)
		}
		seen[name] = true

		image := strings.TrimSpace(s.Image)
		if image == "" {
			return nil, fmt.Errorf("service %q has no image", name)
		}
		if strings.ContainsAny(image, " \t\n") {
			return nil, fmt.Errorf("service %q: %q is not an image reference", name, s.Image)
		}

		var env map[string]string
		for k, v := range s.Env {
			k = strings.TrimSpace(k)
			if !envKey.MatchString(k) {
				return nil, fmt.Errorf("service %q: %q is not an environment variable name", name, k)
			}
			if env == nil {
				env = map[string]string{}
			}
			env[k] = v
		}
		out = append(out, Service{Name: name, Image: image, Env: env})
	}
	return out, nil
}

// ServiceImages are the images a declaration needs on the host, in the order
// they were declared. The runner pulls them; the pool asks with it whether a
// host can carry this agent at all.
func ServiceImages(services []Service) []string {
	out := make([]string, 0, len(services))
	for _, s := range services {
		out = append(out, s.Image)
	}
	return out
}
