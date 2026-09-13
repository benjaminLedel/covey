package sandboxfs

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// The file errors have to survive the journey over the runner link. Without a
// name and a way back, an agent's home on another host would answer every
// mistyped path with a 500 — the HTTP layer maps these sentinels to statuses,
// and an error arriving as bare text carries none of that.
//
// So the round trip is the claim: every sentinel keeps its identity across the
// wire, and what is not one arrives as the runner's own wording rather than as
// a sentinel that fits it badly.
func TestErrorKindsSurviveTheRunnerLink(t *testing.T) {
	for _, sentinel := range []error{
		ErrNotFound, ErrInvalidPath, ErrTooLarge,
		ErrNotDir, ErrIsDir, ErrExists, ErrTooMany,
	} {
		kind := ErrorKind(sentinel)
		if kind == "" {
			t.Errorf("%v travels without a name", sentinel)
			continue
		}
		back := ErrorFromKind(kind, sentinel.Error())
		if !errors.Is(back, sentinel) {
			t.Errorf("%q came back as %v", kind, back)
		}
	}

	// Wrapped, which is how they actually arrive: a path error carrying one of
	// them still has to be recognised.
	wrapped := fmt.Errorf("opening %q: %w", "notizen/x.md", ErrNotFound)
	if got := ErrorKind(wrapped); got != "not_found" {
		t.Errorf("a wrapped sentinel travels as %q", got)
	}

	// Nothing is nothing.
	if got := ErrorKind(nil); got != "" {
		t.Errorf("ErrorKind(nil) = %q", got)
	}

	// Something else keeps its message rather than becoming a sentinel that
	// fits it badly — the reader then sees what the host actually said.
	other := errors.New("the disk is full")
	if got := ErrorKind(other); got != "" {
		t.Errorf("a foreign error was given the name %q", got)
	}
	back := ErrorFromKind("", "the disk is full")
	if back == nil || back.Error() != "the disk is full" {
		t.Errorf("the foreign wording was lost: %v", back)
	}
	for _, sentinel := range []error{ErrNotFound, ErrInvalidPath, ErrTooLarge} {
		if errors.Is(back, sentinel) {
			t.Errorf("a foreign error came back as %v", sentinel)
		}
	}

	// A kind this build does not know is the same case: the message survives.
	future := ErrorFromKind("erfunden_in_der_zukunft", "etwas Neues")
	if future == nil || future.Error() != "etwas Neues" {
		t.Errorf("an unknown kind lost its message: %v", future)
	}
}

// The space situation of a home, as the workspace page shows it. A home that
// has never been created — an agent that has never been woken — says so rather
// than reporting zeros that read as an empty one: the page is opened before
// the first run at least as often as after it.
func TestUsageOfAHomeThatWasNeverCreated(t *testing.T) {
	fs, err := New(t.TempDir()+"/gibtesnicht", -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	u := fs.Usage()
	if u.Exists {
		t.Error("a home that is not there reports that it exists")
	}
	// Checkouts is a list rather than nil, so the interface renders an empty
	// section instead of nothing at all.
	if u.Checkouts == nil {
		t.Error("Checkouts is nil — the page would render nothing rather than an empty list")
	}
}

// A home that is there reports the file system it lies on: exactly the figure
// that decides whether the next npm install still fits.
func TestUsageOfAHomeThatExists(t *testing.T) {
	root := t.TempDir()
	fs, err := New(root, -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write("zwei.md", strings.NewReader("123")); err != nil {
		t.Fatal(err)
	}

	u := fs.Usage()
	if !u.Exists {
		t.Fatal("a home that was written to reports that it does not exist")
	}
	if u.TotalBytes <= 0 || u.FreeBytes <= 0 {
		t.Errorf("the file system reports %d total and %d free", u.TotalBytes, u.FreeBytes)
	}
	if u.FreeBytes > u.TotalBytes {
		t.Errorf("more free than total: %d of %d", u.FreeBytes, u.TotalBytes)
	}
	// Only repos/ is walked — the one directory that grows without bound.
	// Walking the whole home would make the page slow for no gain, so a file
	// outside it counts towards nothing here.
	if u.CheckoutBytes != 0 {
		t.Errorf("a file outside repos/ was counted as a checkout: %d", u.CheckoutBytes)
	}

	if _, err := fs.Mkdir("repos"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkdir("repos/projekt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write("repos/projekt/datei.txt", strings.NewReader("12345")); err != nil {
		t.Fatal(err)
	}
	u = fs.Usage()
	if u.CheckoutBytes < 5 {
		t.Errorf("the working copy weighs %d bytes, expected at least the five written", u.CheckoutBytes)
	}
	if len(u.Checkouts) != 1 || u.Checkouts[0].Name != "projekt" {
		t.Errorf("the working copies are %+v", u.Checkouts)
	}

	if fs.Root() != root {
		t.Errorf("Root = %q, expected %q", fs.Root(), root)
	}
}
