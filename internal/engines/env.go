package engines

import (
	"strings"
)

// Where a layer lands inside the sandbox, and what the run is told.
//
// The store directory on a runner is the operator's business — it sits under
// their data dir and differs between hosts. The path a run reads must not: the
// daemon in the container asks its adapter for a binary, and that answer has to
// be the same on a laptop and on a fleet of runners. Hence a fixed mount point,
// and hence the marker stores its executable RELATIVE to the layer root.
const MountPoint = "/opt/engines"

// ContainerMount is the bind mount for one layer: host path, container path,
// read-only.
//
// Only this engine's directory is mounted, not the store. Mounting the store
// would hand every sandbox a directory of runtime binaries it was not given —
// read-only stops nobody from executing, and a runtime the platform did not
// account for is exactly what the cost figures and the credential pools in
// spec/18 are there to prevent.
func ContainerMount(l Layer) (host, container string) {
	return l.Root, ContainerRoot(l.Engine, l.Version)
}

// ContainerRoot is where a layer is mounted inside a sandbox.
func ContainerRoot(engine, version string) string {
	return MountPoint + "/" + safeSegment(engine) + "/" + safeSegment(version)
}

// ContainerEnv is what to add to a sandbox so its engine is the catalogue's:
// the variable the adapter reads, and whatever else the entry declares (an
// endpoint a CLI takes from its environment).
//
// The credential is NOT among them. Brokered secrets travel the path in
// spec/04 and arrive per run; anything in this list is catalogue content, which
// is why nothing here may be a secret.
func ContainerEnv(l Layer, r Release) []string {
	exe := ContainerRoot(l.Engine, l.Version) + "/" + l.RelExec
	env := []string{r.BinaryEnvName(l.Engine) + "=" + exe}
	for _, kv := range r.Env {
		// A release cannot name the variable the adapter reads, and a second
		// value for one key would win by ordering rather than by decision.
		if strings.HasPrefix(kv, r.BinaryEnvName(l.Engine)+"=") {
			continue
		}
		env = append(env, kv)
	}
	return env
}
