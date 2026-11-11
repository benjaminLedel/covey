package sso

import "testing"

func TestDomainHandling(t *testing.T) {
	for in, want := range map[string]string{
		"Example.ORG": "example.org", "@example.org": "example.org", "ada@Example.org": "example.org", "example.org.": "example.org",
	} {
		if got := NormalizeDomain(in); got != want {
			t.Errorf("NormalizeDomain(%q) = %q, want %q", in, got, want)
		}
	}
	for d, ok := range map[string]bool{
		"example.org": true, "a-b.example.org": true, "localhost": false, "-x.org": false, "ex ample.org": false, "x..org": false,
	} {
		if validDomain(d) != ok {
			t.Errorf("validDomain(%q) = %v", d, !ok)
		}
	}
	if DomainOf("Ada@Example.org") != "example.org" || DomainOf("nobody") != "" {
		t.Error("DomainOf")
	}
}

func TestClaimAddress(t *testing.T) {
	cases := []struct {
		c    claims
		want string
	}{
		{claims{Email: "Ada@Example.org"}, "ada@example.org"},
		{claims{PreferredUsername: "ada"}, ""},
		{claims{PreferredUsername: "ada", UPN: "ada@example.org"}, "ada@example.org"},
	}
	for _, c := range cases {
		if got := c.c.address(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.c, got, c.want)
		}
	}
}
