//go:build !windows

package runner

import (
	"os"
	"syscall"
)

// execSelf starts the new binary in place of this process — same pid, same
// arguments, same environment. Nothing has to restart it, which matters
// because a runner is installed in more ways than one: as a systemd unit that
// would bring it back, and just as often in a terminal or a tmux where nothing
// would.
//
// The connection does not survive it and must not: the sockets Go opens carry
// FD_CLOEXEC, so they close with the exec, and the control plane sees the
// runner go and come back rather than a line that has silently stopped
// answering.
func execSelf() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, os.Environ())
}
