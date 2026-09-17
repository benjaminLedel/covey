package doctor

import (
	"strings"
	"testing"
)

func TestEngineOriginWhereTheTokenIsMissingIsNotSettled(t *testing.T) {
	t.Helper()
	on := EngineOrigin{Engine: "sevencode", Known: true, Binary: "sevencode",
		Version: "1.0.27", Kind: "tarball", AuthEnv: "COVEY_SEVENCODE_ARTIFACT_TOKEN", AuthSet: true}
	if !on.Settled() || on.AssignmentWarning() != "" {
		t.Errorf("a release whose token is on the host is settled: %v / %q", on.Settled(), on.AssignmentWarning())
	}

	off := on
	off.AuthSet = false
	if off.Settled() {
		t.Error("a release whose artefact cannot be downloaded has not settled the question")
	}
	warn := off.AssignmentWarning()
	if warn == "" {
		t.Fatal("somebody putting an agent on this engine has to be told")
	}
	for _, want := range []string{"COVEY_SEVENCODE_ARTIFACT_TOKEN", "runner"} {
		if !strings.Contains(warn, want) {
			t.Errorf("the warning should say %s: %s", want, warn)
		}
	}
	// Not the other warning: the image is not the answer when the catalogue does
	// name this engine.
	if strings.Contains(warn, "image has to carry") {
		t.Errorf("the image is not what ends this: %s", warn)
	}
	// And the detail says which of the two states this is, because the doctor's
	// line is what somebody reads after the fact.
	if !strings.Contains(off.Detail(), "not set on this host") ||
		strings.Contains(on.Detail(), "not set") {
		t.Errorf("Detail should tell the two apart:\n%s\n%s", on.Detail(), off.Detail())
	}
}

// An entry that names a secret of the organisation rather than a variable of the
// host settles the question here, and says nothing as a warning (#289). The value
// belongs to an agent and is resolved at the start on a machine that is not this
// one; a finding that asks an operator to set something on the wrong host is the
// furniture the guard rails warn about — read twice, acted on never.
//
// Whether this agent actually has the secret is answered where the agent and the
// store are both in reach: the assignment handler in internal/httpapi.
func TestEngineOriginASecretOfTheOrganisationIsNotJudgedHere(t *testing.T) {
	secret := EngineOrigin{Engine: "sevencode", Known: true, Binary: "sevencode",
		Version: "1.0.27", Kind: "file", AuthSecret: "sevencode_api_token"}
	if !secret.Settled() {
		t.Error("a secret of the organisation opens the artefact, the CLI does not have to come from the image")
	}
	if w := secret.AssignmentWarning(); w != "" {
		t.Errorf("nothing on this host is missing, so nothing is to be said here: %q", w)
	}
	if !strings.Contains(secret.Detail(), "sevencode_api_token") {
		t.Errorf("the detail should name what the entry asks for, so the doctor's line can be followed: %s", secret.Detail())
	}

	// The fallback next to it does not re-open the finding: what somebody
	// configured comes first, and an old export on one host is not a state to
	// warn about while the secret is there.
	withFallback := secret
	withFallback.AuthEnv = "COVEY_SEVENCODE_DOWNLOAD_TOKEN"
	if !withFallback.Settled() || withFallback.AssignmentWarning() != "" {
		t.Errorf("the secret is the first way and settles it on its own: %v / %q",
			withFallback.Settled(), withFallback.AssignmentWarning())
	}
}
