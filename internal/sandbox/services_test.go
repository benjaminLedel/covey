package sandbox

import "testing"

func TestValidateServicesNormalises(t *testing.T) {
	out, err := ValidateServices([]Service{
		{Name: "  DB  ", Image: " postgres:16 ", Env: map[string]string{" POSTGRES_PASSWORD ": "test"}},
	})
	if err != nil {
		t.Fatalf("ValidateServices: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d services, want 1", len(out))
	}
	if out[0].Name != "db" || out[0].Image != "postgres:16" {
		t.Errorf("not normalised: %+v", out[0])
	}
	if out[0].Env["POSTGRES_PASSWORD"] != "test" {
		t.Errorf("env key not trimmed: %+v", out[0].Env)
	}
}

func TestValidateServicesRejects(t *testing.T) {
	cases := map[string][]Service{
		"a name that is not a host name": {{Name: "My DB", Image: "postgres:16"}},
		"a name starting with a digit":   {{Name: "9db", Image: "postgres:16"}},
		"a reserved name":                {{Name: "localhost", Image: "postgres:16"}},
		"the egress proxy's name":        {{Name: "covey-egress", Image: "postgres:16"}},
		"the same name twice":            {{Name: "db", Image: "postgres:16"}, {Name: "db", Image: "mariadb:11"}},
		"no image":                       {{Name: "db"}},
		"an image with a space":          {{Name: "db", Image: "postgres 16"}},
		"an env key a shell would drop":  {{Name: "db", Image: "postgres:16", Env: map[string]string{"a-b": "c"}}},
	}
	for what, in := range cases {
		if _, err := ValidateServices(in); err == nil {
			t.Errorf("%s was accepted", what)
		}
	}
}

// The cap is a statement about what a test environment is, so it is worth a
// test of its own: one below passes, one above does not.
func TestValidateServicesCap(t *testing.T) {
	var many []Service
	for i := 0; i < MaxServices; i++ {
		many = append(many, Service{Name: string(rune('a'+i)) + "svc", Image: "postgres:16"})
	}
	if _, err := ValidateServices(many); err != nil {
		t.Fatalf("%d services were refused: %v", MaxServices, err)
	}
	many = append(many, Service{Name: "zsvc", Image: "postgres:16"})
	if _, err := ValidateServices(many); err == nil {
		t.Errorf("%d services were accepted", len(many))
	}
}

// An empty declaration is the normal case — every agent that wants no service
// at all goes through here.
func TestValidateServicesEmpty(t *testing.T) {
	out, err := ValidateServices(nil)
	if err != nil || len(out) != 0 {
		t.Fatalf("nil declaration: %v, %v", out, err)
	}
}
