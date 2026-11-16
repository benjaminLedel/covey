//go:build !linux

package main

// protectProcess has nothing to do outside Linux: sandboxes run in Linux
// containers, and a coveyd built for another system only serves development.
func protectProcess() error { return nil }

func processDumpable() bool { return false }
