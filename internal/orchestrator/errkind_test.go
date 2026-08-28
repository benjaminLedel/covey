package orchestrator

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"covey/internal/runtimes"
	"covey/internal/secrets"
)

// TestErrKindNennt pins what the log line about a missing credential may say.
//
// The point is the last case. Everything before it is convenience — a reader
// gets the reason in words instead of an error type. The last one is the actual
// obligation: an error that came from somewhere deep enough that we do not know
// what it carries must reach the log as its TYPE and never as its text. A test
// that only checked the friendly cases would pass while exactly that guarantee
// was removed.
func TestErrKindNennt(t *testing.T) {
	geheim := "sk-ant-oat01-not-in-a-log-line"

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no runtime", errNoRuntime, "no workplace assigned"},
		{"runtime without credential", runtimes.ErrNotFound, "workplace without a credential"},
		{"value gone", fmt.Errorf("value: %w", secrets.ErrNotFound), "the deposited value is gone"},
		{"wrong engine", &runtimes.WrongEngine{Runtime: "Claude", Seat: "claude-code", Agent: "educa-ai"}, "the seat belongs to another engine"},
		{"unknown error", errors.New("decrypt " + geheim), "*errors.errorString"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := errKind(c.err)
			if got != c.want {
				t.Errorf("errKind = %q, expected %q", got, c.want)
			}
			if strings.Contains(got, geheim) {
				t.Errorf("errKind carries the error text into the log: %q", got)
			}
		})
	}

	if got := errKind(nil); got != "" {
		t.Errorf("errKind(nil) = %q, expected empty", got)
	}
}
