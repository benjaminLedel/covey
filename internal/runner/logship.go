package runner

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// Shipping the runner's own log to the control plane.
//
// A runner writes to its stderr, and under systemd that means journald on a
// host somebody has to have a shell on. For the one component that deliberately
// stands on a machine the control plane does not own, that is the wrong place:
// "why did this host stop taking sandboxes at three in the morning" was a
// question only an SSH session could answer, and only for as long as the
// journal kept it.
//
// So the same lines go two ways. The stderr handler stays exactly as it was —
// whoever debugs on the host keeps what they had. Beside it a ring buffer
// collects, and a flush sends what has gathered up the runner link.
//
// It is a ring and not a queue on purpose. A runner that loses its connection
// keeps working and keeps logging; an unbounded buffer would turn a network
// problem into an out-of-memory one, on the host that is currently running
// somebody's sandboxes. What overflows is the OLDEST line, and how many were
// lost travels with the next batch — a number once, instead of a silence that
// reads like quiet.
const (
	// logRingCap is roughly a busy quarter of an hour at debug level.
	logRingCap = 2048
	// logFlushEvery is how often a batch goes up. Not per line: a start at
	// debug level writes hundreds, and a frame each would drown the link the
	// sandboxes are started over.
	logFlushEvery = 2 * time.Second
	// logBatchMax caps one batch, so that a buffer which ran full after a
	// reconnect goes up in several frames instead of one huge one.
	logBatchMax = 256
)

// logRing is the buffer between "the runner writes a line" and "the control
// plane gets a batch".
type logRing struct {
	mu      sync.Mutex
	entries []LogEntry
	dropped int
	// lvl is the level from which a line is SHIPPED — switchable at runtime
	// from the interface. Atomic because the switch arrives on the protocol
	// goroutine while log lines are written on every other one.
	lvl atomic.Int32
}

func newLogRing(level slog.Level) *logRing {
	r := &logRing{entries: make([]LogEntry, 0, 64)}
	r.lvl.Store(int32(level))
	return r
}

func (r *logRing) level() slog.Level { return slog.Level(r.lvl.Load()) }

// setLevel switches what gets shipped. Returns false for a level nobody can
// choose usefully — a typo must not quietly silence a host.
func (r *logRing) setLevel(name string) bool {
	switch name {
	case LogLevelDebug:
		r.lvl.Store(int32(slog.LevelDebug))
	case LogLevelInfo:
		r.lvl.Store(int32(slog.LevelInfo))
	default:
		return false
	}
	return true
}

func (r *logRing) add(e LogEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) >= logRingCap {
		// Drop the oldest. The newest lines are the ones somebody is waiting
		// for; the oldest are the ones they have already read.
		copy(r.entries, r.entries[1:])
		r.entries = r.entries[:logRingCap-1]
		r.dropped++
	}
	r.entries = append(r.entries, e)
}

// take empties the buffer for one flush and reports what was lost since the
// last one.
func (r *logRing) take(max int) ([]LogEntry, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.entries) == 0 {
		return nil, 0
	}
	n := min(len(r.entries), max)
	out := make([]LogEntry, n)
	copy(out, r.entries[:n])
	r.entries = append(r.entries[:0], r.entries[n:]...)
	dropped := r.dropped
	r.dropped = 0
	return out, dropped
}

// putBack returns entries a flush could not deliver to the front of the ring,
// so they go with the next batch instead of vanishing uncounted (#165). What
// does not fit any more is dropped as the oldest, and counted.
func (r *logRing) putBack(entries []LogEntry, dropped int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dropped += dropped
	room := logRingCap - len(r.entries)
	if room < len(entries) {
		r.dropped += len(entries) - room
		entries = entries[len(entries)-room:]
	}
	r.entries = append(append(make([]LogEntry, 0, len(entries)+len(r.entries)), entries...), r.entries...)
}

// shipHandler is an slog.Handler that passes everything to the handler it
// wraps and additionally records it for the control plane.
type shipHandler struct {
	inner slog.Handler
	ring  *logRing
	attrs []slog.Attr
	group string
}

// shippingLogger wraps a logger so that its lines also go up the runner link.
func shippingLogger(log *slog.Logger, ring *logRing) *slog.Logger {
	return slog.New(&shipHandler{inner: log.Handler(), ring: ring})
}

// Enabled has to answer for BOTH consumers: a line the stderr handler would
// drop can still be one the interface asked for by switching to debug. Saying
// no here would mean the switch changes nothing, which is the failure mode
// that is hardest to see — the level says debug and the lines never come.
func (h *shipHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l) || l >= h.ring.level()
}

func (h *shipHandler) Handle(ctx context.Context, r slog.Record) error {
	var innerErr error
	if h.inner.Enabled(ctx, r.Level) {
		innerErr = h.inner.Handle(ctx, r)
	}
	if r.Level < h.ring.level() {
		return innerErr
	}
	e := LogEntry{Time: r.Time, Level: levelName(r.Level), Msg: r.Message}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	add := func(a slog.Attr) {
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		if key == "agent" || key == "agent_id" {
			// A line about a start belongs to the agent it is about. Tying it
			// here rather than at the reader means the interface can show a
			// runner's log filtered to one agent without parsing text.
			if id, err := uuid.Parse(a.Value.String()); err == nil {
				e.AgentID = id
				return
			}
		}
		if e.Attrs == nil {
			e.Attrs = map[string]string{}
		}
		e.Attrs[key] = a.Value.String()
	}
	for _, a := range h.attrs {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool { add(a); return true })
	h.ring.add(e)
	return innerErr
}

func (h *shipHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.inner = h.inner.WithAttrs(attrs)
	next.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &next
}

func (h *shipHandler) WithGroup(name string) slog.Handler {
	next := *h
	next.inner = h.inner.WithGroup(name)
	if h.group != "" {
		next.group = h.group + "." + name
	} else {
		next.group = name
	}
	return &next
}

// levelName keeps the wire format readable and stable. slog's own String()
// would produce "DEBUG+2" for a custom level, and a level nobody can filter on
// is a level nobody reads.
func levelName(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "debug"
	case l < slog.LevelWarn:
		return "info"
	case l < slog.LevelError:
		return "warn"
	default:
		return "error"
	}
}

// shipLogs flushes the buffer up the link until ctx ends. Failures to send are
// not reported: the line about a broken connection would be the next one this
// same function fails to deliver.
func (n *Node) shipLogs(ctx context.Context, t Transport) {
	tick := time.NewTicker(logFlushEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			// The last flush is Run's, synchronously, before the transport is
			// closed — from here it would race that close (#165).
			return
		case <-tick.C:
			n.flushLogs(ctx, t)
		}
	}
}

// flushLogs sends what has gathered. What cannot be sent goes back into the
// ring: the lines a runner wrote just before its link went are precisely the
// ones worth having, and they go up with the next connection.
func (n *Node) flushLogs(ctx context.Context, t Transport) {
	for {
		entries, dropped := n.logs.take(logBatchMax)
		if len(entries) == 0 && dropped == 0 {
			return
		}
		msg, err := encode(TypeLog, "", LogBatch{Entries: entries, Dropped: dropped})
		if err != nil {
			return
		}
		if err := t.Send(ctx, msg); err != nil {
			n.logs.putBack(entries, dropped)
			return
		}
		if len(entries) < logBatchMax {
			return
		}
	}
}
