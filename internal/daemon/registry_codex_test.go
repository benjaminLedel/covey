package daemon

import "testing"

func TestCodexIsRegistered(t *testing.T) {
	d, ok := Describe("codex")
	if !ok {
		t.Fatal("codex is not registered")
	}
	if !d.NeedsCredential() || len(d.Credentials) != 2 {
		t.Fatalf("credentials: %+v", d.Credentials)
	}
	api, _ := d.Credential(CredAPIKey)
	if api.EnvVar != "CODEX_API_KEY" || api.Path != "" {
		t.Fatalf("the API key is an environment variable: %+v", api)
	}
	sub, _ := d.Credential(CredSubscription)
	if sub.Path == "" || sub.EnvVar != "" {
		t.Fatalf("the ChatGPT plan is a file: %+v", sub)
	}
	if d.Capabilities.Resume {
		t.Fatal("resume is unverified and must stay false until it is checked")
	}
}

// TestCredentialDeliveryIsDeclaredNotGuessed: the assumption that a brokered
// credential is always an environment variable broke at the second engine. Both
// forms have to be expressible, and exactly one of them set per credential.
func TestCredentialDeliveryIsDeclaredNotGuessed(t *testing.T) {
	for _, d := range Runtimes() {
		for _, c := range d.Credentials {
			if (c.EnvVar == "") == (c.Path == "") {
				t.Fatalf("%s/%s: exactly one of EnvVar and Path has to be set (%+v)",
					d.Name, c.Kind, c)
			}
			if c.Secret == "" {
				t.Fatalf("%s/%s: without a secret name nothing can be looked up", d.Name, c.Kind)
			}
		}
		// NeedsCredential is derived, never declared twice.
		if d.NeedsCredential() != (len(d.Credentials) > 0) {
			t.Fatalf("%s: NeedsCredential has to follow from the declaration", d.Name)
		}
	}
}

// TestSafeCredentialPath keeps a declared path inside the home. The declaration
// is our own code, so this is the second door — but whoever writes to a file
// system checks.
func TestSafeCredentialPath(t *testing.T) {
	for _, ok := range []string{".codex/auth.json", "a/b/c.json"} {
		if !safeCredentialPath(ok) {
			t.Fatalf("%q should be allowed", ok)
		}
	}
	for _, bad := range []string{"", "/etc/passwd", "../escape", "a/../../b", "a//b", "a/./b"} {
		if safeCredentialPath(bad) {
			t.Fatalf("%q must be refused", bad)
		}
	}
}
