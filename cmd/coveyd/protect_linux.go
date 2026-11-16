package main

import "syscall"

// prSetDumpable and prGetDumpable are the prctl options of <linux/prctl.h>.
const (
	prSetDumpable = 4
	prGetDumpable = 3
)

// protectProcess marks coveyd as not dumpable. The runtime, its tools and
// every process a plugin starts run under the same user as coveyd, and for a
// dumpable process that user may read /proc/<pid>/environ and /proc/<pid>/mem
// and attach a debugger. Both hold what the sandbox must not hand out: the
// daemon's token for the control plane and the credentials the broker injects
// for a call. A process that is not dumpable has those files owned by root
// and refuses ptrace from anyone without CAP_SYS_PTRACE, which the sandbox
// drops (--cap-drop=ALL). Children start dumpable again on exec, so nothing
// the agent runs is affected.
func protectProcess() error {
	_, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func processDumpable() bool {
	r, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, prGetDumpable, 0, 0)
	return r != 0
}
