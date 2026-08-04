package buildinfo

import "testing"

func TestInfoString(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "complete",
			in:   Info{Version: "v0.1.0", Commit: "abc1234", BuiltAt: "2026-07-30T12:00:00Z", Go: "go1.26"},
			want: "v0.1.0 (abc1234, 2026-07-30T12:00:00Z, go1.26)",
		},
		{
			name: "working tree with changes",
			in:   Info{Version: "dev", Commit: "abc1234", Dirty: true, Go: "go1.26"},
			want: "dev (abc1234-dirty, go1.26)",
		},
		{
			name: "without any provenance",
			in:   Info{Version: "dev"},
			want: "dev",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.in.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Without -ldflags Get() still carries a version — "dev" instead of empty, so
// that the UI never shows an empty line.
func TestGetAlwaysHasAVersion(t *testing.T) {
	if got := Get().Version; got == "" {
		t.Error("version is empty")
	}
	if got := Get().Go; got == "" {
		t.Error("Go version is empty")
	}
}
