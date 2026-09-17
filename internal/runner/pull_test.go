package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* The image pull is the longest wait the platform has, and was the
   quietest one: `docker run` fetches a missing image by itself, several gigabytes,
   and says nothing until the end. The numbers for it stand in docker's own
   progress lines — here we read whether we get them out as well. */

func TestFortschrittszeilenLesen(t *testing.T) {
	faelle := []struct {
		zeile string
		id    string
		bytes int64
		total int64
		ok    bool
	}{
		{"a1b2c3d4: Downloading [====>      ]  1.2GB/3.4GB", "a1b2c3d4", 1_200_000_000, 3_400_000_000, true},
		{"ff00ee11: Extracting [==========>]  45.5MB/45.5MB", "ff00ee11", 45_500_000, 45_500_000, true},
		{"abc: Downloading [>          ]  512B/1.5kB", "abc", 512, 1500, true},
		// Not progress but prose — and must not be counted as zero, or the
		// sum shrinks in the middle of the pull.
		{"a1b2c3d4: Pull complete", "", 0, 0, false},
		{"latest: Pulling from covey/sandbox", "", 0, 0, false},
		{"Status: Downloaded newer image for covey/sandbox:latest", "", 0, 0, false},
		{"", "", 0, 0, false},
	}
	for _, f := range faelle {
		id, pr, ok := parsePullLine(f.zeile)
		if ok != f.ok {
			t.Fatalf("%q: ok=%v, erwartet %v", f.zeile, ok, f.ok)
		}
		if !ok {
			continue
		}
		if id != f.id || pr.Bytes != f.bytes || pr.Total != f.total {
			t.Fatalf("%q: %s %d/%d, erwartet %s %d/%d", f.zeile, id, pr.Bytes, pr.Total, f.id, f.bytes, f.total)
		}
	}
}

// Several layers run at the same time; what the human wants to see is the
// image, not the layer. So the sum — and that with the newest
// state of each layer, not with their sum over time.
func TestPullMeldetDieSummeUeberDieSchichten(t *testing.T) {
	skript := `#!/bin/sh
echo "latest: Pulling from covey/sandbox"
echo "aaaa: Downloading [=>         ]  100MB/1GB"
echo "bbbb: Downloading [=>         ]  200MB/2GB"
echo "aaaa: Downloading [=====>     ]  500MB/1GB"
echo "aaaa: Pull complete"
echo "Status: Downloaded newer image for covey/sandbox:latest"
`
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Docker{DockerBin: bin}

	var letzte PullProgress
	var meldungen int
	out, err := p.PullWatched(context.Background(), "covey/sandbox:latest", func(pr PullProgress) {
		letzte = pr
		meldungen++
	})
	if err != nil {
		t.Fatalf("pull: %v (%s)", err, out)
	}
	if meldungen != 3 {
		t.Fatalf("%d Meldungen, erwartet 3 (nur die Fortschrittszeilen)", meldungen)
	}
	// 500 MB from aaaa + 200 MB from bbbb, of 1 GB + 2 GB.
	if letzte.Bytes != 700_000_000 || letzte.Total != 3_000_000_000 {
		t.Fatalf("Summe %d/%d, erwartet 700000000/3000000000", letzte.Bytes, letzte.Total)
	}
	if out == "" {
		t.Fatal("die Ausgabe fehlt — bei einem Fehlschlag steht der Grund genau darin")
	}
}

// A failed pull gives out its reason: no credentials for
// a private registry, a typo in the reference, no route to the host —
// three different humans that this concerns.
func TestPullGibtDenGrundHeraus(t *testing.T) {
	skript := "#!/bin/sh\necho 'Error response from daemon: pull access denied' >&2\nexit 1\n"
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte(skript), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &Docker{DockerBin: bin}
	out, err := p.PullWatched(context.Background(), "private/sandbox:1", nil)
	if err == nil {
		t.Fatal("der Fehlschlag wurde verschluckt")
	}
	if !strings.Contains(out, "access denied") {
		t.Fatalf("der Grund fehlt in der Ausgabe: %q", out)
	}
}
