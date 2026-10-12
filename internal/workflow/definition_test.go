package workflow

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTheExampleParsesAndPassesTheChecks(t *testing.T) {
	src, err := os.ReadFile("testdata/customer-ticket.workflow")
	if err != nil {
		t.Fatal(err)
	}
	d, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	if ps := Check(d); len(ps) > 0 {
		t.Fatalf("the example has problems: %v", ps)
	}
	if got := strings.Join(d.Order, " "); got != "t b r a c s w e h n" {
		t.Errorf("order = %q, want the file's", got)
	}
	b := d.Steps["b"]
	if b.Limit != 4*time.Hour || !b.Sticky || b.MaxParallel != 2 || b.Pick != PickLoad {
		t.Errorf("build step read wrongly: %+v", b)
	}
	if d.Steps["w"].Max != 14*24*time.Hour {
		t.Errorf("14d read as %s", d.Steps["w"].Max)
	}
	if got := strings.Join(d.Steps["h"].Options, ","); got != "retry,self" {
		t.Errorf("approval options = %q, want them in the file's order", got)
	}
	if got := LoopExits(d, "c"); len(got) != 1 || got[0] != "defects" {
		t.Errorf("loop exits of c = %v, want [defects]", got)
	}
}

func TestParseRefusesWhatItCannotRead(t *testing.T) {
	for name, src := range map[string]string{
		"unknown type": "steps:\n  t:\n    type: teleport\n",
		"unknown mode": "mode: sometimes\nsteps:\n  t:\n    type: trigger\n",
		"bad duration": "steps:\n  b:\n    type: agent\n    limit: soon\n",
		"bad days":     "steps:\n  w:\n    type: wait\n    max: xd\n",
		"unknown pick": "steps:\n  b:\n    type: agent\n    pick: loudest\n",
		"nested exit":  "steps:\n  c:\n    type: condition\n    next:\n      a: [x, y]\n",
		"no steps":     "workflow: empty\n",
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("%s: parsed without an error", name)
		}
	}
}

// Each broken definition is the smallest that shows one problem. The table
// names the code Check must report for it.
func TestCheckNamesWhatKeepsAWorkflowFromRunning(t *testing.T) {
	const head = "roles:\n  R: [ada]\nsteps:\n"
	cases := []struct {
		name, steps, code string
	}{
		{"no trigger", "  e:\n    type: end\n", "no-trigger"},
		{"two triggers", "  t:\n    type: trigger\n    next: e\n  u:\n    type: trigger\n    next: e\n  e:\n    type: end\n", "many-triggers"},
		{"unknown target", "  t:\n    type: trigger\n    next: nowhere\n", "unknown-target"},
		{"leads nowhere", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n", "no-exit"},
		{"unreachable", "  t:\n    type: trigger\n    next: e\n  e:\n    type: end\n  x:\n    type: end\n", "unreachable"},
		{"unknown role", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: Nobody\n    next: e\n  e:\n    type: end\n", "unknown-role"},
		{"back into the trigger", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n    next: t\n", "into-trigger"},
		{"four-eyes on a non-colleague step", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n    not_same_as: t\n    next: e\n  e:\n    type: end\n", "four-eyes"},
		{"condition on nothing", "  t:\n    type: trigger\n    next: c\n  c:\n    type: condition\n    on: nowhere.x\n    next:\n      y: e\n  e:\n    type: end\n", "bad-field"},
		{"loop without a limit", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n    next: c\n  c:\n    type: condition\n    on: b.ok\n    next:\n      yes: e\n      no: b\n  e:\n    type: end\n", "loop-without-limit"},
		{"limit without its exit", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n    next: c\n  c:\n    type: condition\n    on: b.ok\n    max_rounds: 2\n    next:\n      yes: e\n      no: b\n  e:\n    type: end\n", "loop-without-limit"},
		{"loop past no condition", "  t:\n    type: trigger\n    next: b\n  b:\n    type: agent\n    role: R\n    next: n\n  n:\n    type: notify\n    to: ada\n    next: b\n", "uncounted-loop"},
		{"endless wait", "  t:\n    type: trigger\n    next: w\n  w:\n    type: wait\n    next: e\n  e:\n    type: end\n", "endless-wait"},
		{"approval without a person", "  t:\n    type: trigger\n    next: h\n  h:\n    type: approval\n    next:\n      ok: e\n  e:\n    type: end\n", "no-person"},
		{"approval deadline without its exit", "  t:\n    type: trigger\n    next: h\n  h:\n    type: approval\n    who: ada\n    wait: 2d\n    next:\n      ok: e\n  e:\n    type: end\n", "no-timeout-exit"},
		{"end with an exit", "  t:\n    type: trigger\n    next: e\n  e:\n    type: end\n    next: t\n", "end-has-exits"},
	}
	for _, c := range cases {
		d, err := Parse([]byte(head + c.steps))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var codes []string
		for _, p := range Check(d) {
			codes = append(codes, p.Code)
		}
		found := false
		for _, code := range codes {
			found = found || code == c.code
		}
		if !found {
			t.Errorf("%s: want %s among %v", c.name, c.code, codes)
		}
	}
}

func TestNewRefusesADefinitionWithProblems(t *testing.T) {
	d, err := Parse([]byte("steps:\n  e:\n    type: end\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(d, &world{}, nil); err == nil || !strings.Contains(err.Error(), "no-trigger") {
		t.Fatalf("New = %v, want it to name the problem", err)
	}
}
