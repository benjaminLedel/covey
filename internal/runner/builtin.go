package runner

import "strings"

// Whether the control plane may carry sandboxes itself is a policy, and it used
// to live as an `if` inside a closure in cmd/covey — the one place no test
// reaches. It cost covey.work its data plane twice in one afternoon: first
// because pick() would not fall back to the built-in runner, then, after that
// was fixed, because the same rule sat a second time in EnsureLocal and
// refused there. Both times the message was reasonable and the outcome was an
// organisation whose agents waited.
//
// So it is a named function with a test now. It is thin on purpose: the value
// is not in the branching, it is in there being ONE place that decides and one
// place to read when the answer surprises somebody.

// BuiltinModeOff is the value of COVEY_BUILTIN_RUNNER that keeps sandboxes off
// the control plane's machine come what may.
const BuiltinModeOff = "off"

// BuiltinAllowed says whether a built-in runner may be brought up for an
// organisation.
//
// The default ("auto", and anything else): yes. The pool asks only when no
// CONNECTED runner fits the workplace, so the fallback is the difference
// between a sandbox on this machine and no sandbox at all — and it cannot
// smuggle work past a requirement, because pick runs again afterwards and tags
// still exclude a host that does not carry them.
//
// "off": no, as soon as the organisation has a registered runner. For an
// installation whose compute must not touch the control plane's host. Said
// once and deliberately, rather than following silently from the existence of
// another machine.
func BuiltinAllowed(mode string, hasRemote bool) bool {
	if !hasRemote {
		// Without a registered runner there is nothing else that could carry
		// the sandbox — "off" would mean an organisation that can never work.
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(mode), BuiltinModeOff)
}
