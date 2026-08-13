package reqlog

import "github.com/benjaminLedel/covey-plugin-sdk/target"

// Plugins take their HTTP client from the SDK, and this is what makes that
// worth doing: their traffic ends up in the request log like everything else at
// the platform's edges.
//
// The wiring sits HERE rather than in the binaries' main, and that is the
// point: whoever links in the request log gets the recording of plugin traffic
// with it — the control plane, the sandbox daemon, and the test stack alike.
// In cmd it was a line two of three callers forgot, and a plugin whose traffic
// is invisible is the worst way for a target system to misbehave.
func init() { target.SetClientFactory(Client) }
