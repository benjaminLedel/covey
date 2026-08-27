package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* Der Bild-Abruf ist die längste Wartezeit, die die Plattform hat, und war die
   stillste: `docker run` holt ein fehlendes Image selbst, mehrere Gigabyte, und
   sagt bis zum Ende nichts. Die Zahlen dafür stehen in dockers eigenen
   Fortschrittszeilen — hier wird gelesen, ob wir sie auch herausholen. */

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
		// Kein Fortschritt, sondern Prosa — und darf nicht als Null gezählt
		// werden, sonst schrumpft die Summe mitten im Abruf.
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

// Mehrere Schichten laufen gleichzeitig; was der Mensch sehen will, ist das
// Bild, nicht die Schicht. Also die Summe — und zwar mit dem jeweils neuesten
// Stand jeder Schicht, nicht mit ihrer Summe über die Zeit.
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
	// 500 MB von aaaa + 200 MB von bbbb, von 1 GB + 2 GB.
	if letzte.Bytes != 700_000_000 || letzte.Total != 3_000_000_000 {
		t.Fatalf("Summe %d/%d, erwartet 700000000/3000000000", letzte.Bytes, letzte.Total)
	}
	if out == "" {
		t.Fatal("die Ausgabe fehlt — bei einem Fehlschlag steht der Grund genau darin")
	}
}

// Ein fehlgeschlagener Abruf gibt seinen Grund heraus: keine Zugangsdaten für
// eine private Registry, ein Tippfehler in der Referenz, keine Route zum Host —
// drei verschiedene Menschen, die das angeht.
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
