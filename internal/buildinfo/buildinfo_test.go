package buildinfo

import "testing"

func TestInfoString(t *testing.T) {
	cases := []struct {
		name string
		in   Info
		want string
	}{
		{
			name: "vollständig",
			in:   Info{Version: "v0.1.0", Commit: "abc1234", BuiltAt: "2026-07-30T12:00:00Z", Go: "go1.26"},
			want: "v0.1.0 (abc1234, 2026-07-30T12:00:00Z, go1.26)",
		},
		{
			name: "arbeitsbaum mit änderungen",
			in:   Info{Version: "dev", Commit: "abc1234", Dirty: true, Go: "go1.26"},
			want: "dev (abc1234-dirty, go1.26)",
		},
		{
			name: "ohne jede herkunft",
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

// Ohne -ldflags trägt Get() mindestens eine Version — "dev" statt leer, damit
// die UI nie eine leere Zeile zeigt.
func TestGetHatImmerEineVersion(t *testing.T) {
	if got := Get().Version; got == "" {
		t.Error("Version ist leer")
	}
	if got := Get().Go; got == "" {
		t.Error("Go-Version ist leer")
	}
}
