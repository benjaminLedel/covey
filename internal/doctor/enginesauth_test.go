package doctor

import (
	"strings"
	"testing"
)

func TestEngineOriginATokenThatIsNotThereIsNotSettled(t *testing.T) {
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
