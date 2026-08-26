package runner

import "errors"

// Windows has no exec that replaces the running process, and covey-runner is
// not published for it. Rather than fake it with a spawn-and-exit that would
// leave a half-updated host behind: the binary is replaced, and whoever runs it
// there restarts it themselves.
func execSelf() error {
	return errors.New("the binary was replaced — restart the runner by hand (no exec on this platform)")
}
