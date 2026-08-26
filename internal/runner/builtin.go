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
// "off": never, for an installation whose compute must not touch the control
// plane's host.
//
// The existence of another runner used to be part of this answer: "off" meant
// "off as soon as the organisation has a registered one", and the default meant
// the built-in stood down by itself in that case. Both are gone, and paused is
// what replaced them. The rule inferred an intention from a fact — somebody
// registered a host, therefore they want nothing on this machine — and the
// inference was wrong often enough to be expensive: registering a runner cost
// covey.work its data plane twice in one afternoon, and a host that was
// connected but stuck cost it a night, because stepping in required there to be
// no candidate at all.
//
// A pause says the same thing without guessing: it is set on the built-in
// runner, by a person, it is visible in the runner view, it survives a restart,
// and it can be taken back in the same place. What used to be a rule nobody
// could see is now a state everybody can.
func BuiltinAllowed(mode string) bool {
	return !strings.EqualFold(strings.TrimSpace(mode), BuiltinModeOff)
}
