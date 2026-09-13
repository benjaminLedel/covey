package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"testing"
)

// captureStdout runs f with stdout redirected and returns what it printed.
// The CLI's output IS its interface, so it is what a test has to look at.
func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		io.Copy(&buf, r)
		done <- buf.String()
	}()
	f()
	w.Close()
	os.Stdout = old
	return <-done
}

// exec_lookPandoc says whether the optional converter is on the PATH. Tests
// that describe its absence skip when it happens to be installed.
func exec_lookPandoc() (string, error) { return exec.LookPath("pandoc") }
