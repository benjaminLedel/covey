package main

import "testing"

// validateRef/validateDevice are the actual security boundary here: ref and
// device end up as argv entries to git/xcodebuild/simctl. exec.Command never
// invokes a shell, so there is no shell-injection surface — but a value
// starting with "-" could still be misread as a flag by the tool itself
// (e.g. git checkout --upload-pack=/some/evil/script). Both patterns must
// reject that shape outright, not just "look strict".
func TestValidateRef(t *testing.T) {
	valid := []string{"main", "feature/45-something", "v1.2.3", "234-tenant-settings-stripe-sync"}
	for _, ref := range valid {
		if err := validateRef(ref); err != nil {
			t.Errorf("validateRef(%q) rejected a normal ref: %v", ref, err)
		}
	}
	invalid := []string{
		"", "-upload-pack=/tmp/evil.sh", "--help", " main", "main;rm -rf /",
		"main`whoami`", "$(whoami)", "../../etc/passwd",
	}
	for _, ref := range invalid {
		if err := validateRef(ref); err == nil {
			t.Errorf("validateRef(%q) accepted a value that could be misread as a flag or escape the workdir", ref)
		}
	}
}

func TestValidateDevice(t *testing.T) {
	valid := []string{"StockiTest17Pro", "iPhone 16", "iPhone 16 Pro Max"}
	for _, d := range valid {
		if err := validateDevice(d); err != nil {
			t.Errorf("validateDevice(%q) rejected a normal device name: %v", d, err)
		}
	}
	invalid := []string{"", "--list", "-x", "device;rm -rf /"}
	for _, d := range invalid {
		if err := validateDevice(d); err == nil {
			t.Errorf("validateDevice(%q) accepted a value that could be misread as a flag", d)
		}
	}
}

func TestTailLines(t *testing.T) {
	log := "l1\nl2\nl3\nl4\nl5"
	if got := tailLines(log, 2); got != "l4\nl5" {
		t.Errorf("tailLines(_, 2) = %q, want %q", got, "l4\nl5")
	}
	if got := tailLines(log, 100); got != log {
		t.Errorf("tailLines with n larger than the log must return it unchanged")
	}
}
