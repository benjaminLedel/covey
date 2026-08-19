package wasmplug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	// callTimeout bounds one invocation. A plugin that has not answered by
	// then is not slow, it is stuck — and an agent waiting on it is an agent
	// not doing anything else.
	callTimeout = 60 * time.Second
	// maxPages caps the module's linear memory (64 KiB per page → 64 MiB). A
	// target-system plugin shuffles JSON; anything beyond this is a runaway.
	maxPages = 1024
	// maxLine bounds one protocol line, so a module cannot exhaust the host's
	// memory by writing forever without a newline.
	maxLine = 8 << 20
	// maxFetches bounds one invocation's requests. A plugin that needs more
	// than this for a single action is looping, and a loop of brokered calls is
	// a loop against somebody's live system.
	maxFetches = 64
)

// Fetcher performs one HTTP request on the module's behalf: the host adds the
// base URL and the brokered credential, so the module never holds either.
type Fetcher func(ctx context.Context, req FetchRequest) FetchResponse

// Module is a compiled plugin. Compiling is the expensive part (wazero turns
// wasm into machine code), so it happens once per module and every invocation
// gets a fresh instance — fresh because a plugin must not carry state from one
// agent's action into another's.
type Module struct {
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	desc     Description

	mu     sync.Mutex
	closed bool
}

// Compile prepares a wasm module for use and asks it what it is.
func Compile(ctx context.Context, wasmBytes []byte) (*Module, error) {
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(maxPages).
		// No filesystem, no sockets, no clock manipulation: whatever the module
		// wants from the outside world, it has to ask the host for.
		WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasm: wasi: %w", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		rt.Close(ctx)
		return nil, fmt.Errorf("wasm: compile: %w", err)
	}
	m := &Module{runtime: rt, compiled: compiled}

	desc, err := m.describe(ctx)
	if err != nil {
		m.Close(ctx)
		return nil, err
	}
	if desc.Name == "" {
		m.Close(ctx)
		return nil, errors.New("wasm: the module describes no name")
	}
	m.desc = desc
	return m, nil
}

// Describe returns what the module said about itself when it was loaded.
func (m *Module) Describe() Description { return m.desc }

func (m *Module) Close(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.runtime.Close(ctx)
}

func (m *Module) describe(ctx context.Context) (Description, error) {
	// describe involves no target system, so it gets a fetcher that refuses:
	// a module asking for credentials before anybody granted any is a module
	// doing something it should not.
	out, err := m.Invoke(ctx, Invocation{Op: "describe"}, Host{Fetch: func(context.Context, FetchRequest) FetchResponse {
		return FetchResponse{Error: "no target system is available while describing"}
	}})
	if err != nil {
		return Description{}, err
	}
	if out.Describe == nil {
		return Description{}, errors.New("wasm: the module answered describe with nothing")
	}
	return *out.Describe, nil
}

// Invoke runs one invocation to its end and returns the terminating message
// (result or error). fetch performs the module's requests; it may be nil, in
// which case every request is refused.
// Host is everything a module may ask of the world during one invocation. A
// struct rather than a growing parameter list, because every field in here is a
// capability: what is nil is not available, and "this module had no workspace"
// is exactly the sentence somebody debugging wants to be able to read.
type Host struct {
	// Fetch performs a request against the brokered target system.
	Fetch Fetcher
	// ReadFile serves a file out of the agent's workspace. nil = there is
	// none, or the module never declared that it reads any.
	ReadFile FileReader
}

func (m *Module) Invoke(ctx context.Context, in Invocation, host Host) (Message, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return Message{}, errors.New("wasm: module is closed")
	}

	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	// stdin carries the invocation and the answers to fetches; stdout carries
	// the module's messages. Two pipes rather than buffers, because the
	// conversation goes back and forth.
	hostToGuest, guestStdin := io.Pipe()
	guestStdout, hostFromGuest := io.Pipe()
	var stderr bytes.Buffer

	line, err := json.Marshal(in)
	if err != nil {
		return Message{}, err
	}

	runErr := make(chan error, 1)
	go func() {
		cfg := wazero.NewModuleConfig().
			WithStdin(hostToGuest).
			WithStdout(hostFromGuest).
			WithStderr(&stderr).
			// A module may know what time it is. wazero's default is a clock
			// frozen at 2022-01-01, which is the right default for a runtime
			// that wants reproducible execution and the wrong one here: a
			// plugin that cannot read the wall clock cannot write a timestamp,
			// and the failure is silent rather than loud. The rollout restart
			// is the case that proves it — it bumps an annotation that HAS to
			// differ from the last one, so a frozen clock turns the second
			// call into a no-op that reports success.
			//
			// It grants nothing else. The module still has no socket, no
			// filesystem and no credential, and the wall time of a machine it
			// cannot reach is not a secret. What it does NOT get is a
			// filesystem clock or a random source — neither has a use that
			// this class of plugin has asked for.
			WithSysWalltime().
			WithSysNanotime().
			WithName(""). // anonymous: several instances may run at once
			WithStartFunctions("_start")
		mod, err := m.runtime.InstantiateModule(ctx, m.compiled, cfg)
		if mod != nil {
			mod.Close(ctx)
		}
		// The pipes have to be closed whatever happened, or the reader below
		// waits for a module that has already gone.
		hostFromGuest.CloseWithError(io.EOF)
		hostToGuest.CloseWithError(io.EOF)
		runErr <- err
	}()

	// Hand over the invocation.
	go func() {
		guestStdin.Write(append(line, '\n'))
	}()

	reader := bufio.NewReaderSize(guestStdout, 64<<10)
	var final Message
	var fetches, reads int
	var protoErr error

	for {
		raw, err := readLine(reader)
		if err != nil {
			break // module ended; whether that is fine is decided below
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			continue
		}
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			protoErr = fmt.Errorf("wasm: module wrote a line that is not a message: %.200s", raw)
			break
		}
		switch {
		case msg.Fetch != nil:
			fetches++
			if fetches > maxFetches {
				protoErr = fmt.Errorf("wasm: module made more than %d requests in one action", maxFetches)
				break
			}
			resp := FetchResponse{Error: "no target system available"}
			if host.Fetch != nil {
				resp = host.Fetch(ctx, *msg.Fetch)
			}
			if err := answer(guestStdin, resp); err != nil {
				protoErr = err
			}
		case msg.ReadFile != nil:
			reads++
			if reads > maxReads {
				protoErr = fmt.Errorf("wasm: module read more than %d files in one action", maxReads)
				break
			}
			resp := ReadFileResponse{Error: "read_file: this plugin has no workspace — it did not declare one, or the action runs outside a sandbox"}
			if host.ReadFile != nil {
				resp = host.ReadFile(ctx, *msg.ReadFile)
			}
			if err := answer(guestStdin, resp); err != nil {
				protoErr = err
			}
		case msg.Log != "":
			// Diagnostics are collected, not forwarded: they belong to the
			// operator, not to the agent's context.
			stderr.WriteString(msg.Log + "\n")
			continue
		default:
			final = msg
		}
		if protoErr != nil || answered(final) {
			break
		}
	}
	guestStdin.Close()
	guestStdout.Close()

	execErr := <-runErr
	if protoErr != nil {
		return Message{}, protoErr
	}
	if final.Error != "" {
		return final, nil // the plugin's own, deliberate failure
	}
	if !answered(final) {
		if execErr != nil {
			return Message{}, fmt.Errorf("wasm: %w%s", execErr, tail(stderr.String()))
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return Message{}, fmt.Errorf("wasm: module did not answer within %s", callTimeout)
		}
		return Message{}, fmt.Errorf("wasm: module ended without an answer%s", tail(stderr.String()))
	}
	return final, nil
}

// answer writes one host reply onto the module's stdin.
func answer(w io.Writer, v any) error {
	raw, _ := json.Marshal(v)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("wasm: module stopped reading: %w", err)
	}
	return nil
}

// answered reports whether a message ends the invocation. Every terminal kind
// belongs in here — a new op that returns a new kind of message and forgets
// this line looks to the host exactly like a module that died silently.
func answered(m Message) bool {
	return m.Result != nil || m.Error != "" || m.Describe != nil || m.Event != nil
}

// readLine reads one protocol line with a cap, so a module that never writes a
// newline cannot pull the host's memory down with it.
func readLine(r *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, more, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("wasm: line longer than %d bytes", maxLine)
		}
		if !more {
			return buf, nil
		}
	}
}

// tail appends a module's diagnostic output to an error, shortened — it is a
// hint for whoever has to fix the plugin, not a log.
func tail(s string) string {
	s = trimSpace(s)
	if s == "" {
		return ""
	}
	if len(s) > 400 {
		s = "…" + s[len(s)-400:]
	}
	return " (module output: " + s + ")"
}

func trimSpace(s string) string { return string(bytes.TrimSpace([]byte(s))) }
