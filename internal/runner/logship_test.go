package runner

import (
	"context"
	"log/slog"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// What a runner writes has to arrive at the control plane — that is the whole
// point of shipping it. The host's own handler must keep getting it too:
// whoever debugs on the machine had a log before this feature and must still
// have one afterwards.
func TestLogLinesGoBothWays(t *testing.T) {
	control, nodeSide := NewInProc()
	defer control.Close()

	runnerID, orgID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{RunnerID: runnerID, DataDir: t.TempDir()}, quietLog())
	t.Cleanup(node.Close)

	node.Log.Info("something happened", "agent", uuid.Nil.String(), "count", 7)
	node.flushLogs(context.Background(), nodeSide)

	batch, err := decode[LogBatch](receiveType(t, control, TypeLog))
	if err != nil {
		t.Fatalf("batch not readable: %v", err)
	}
	if len(batch.Entries) != 1 {
		t.Fatalf("expected one line, got %d", len(batch.Entries))
	}
	e := batch.Entries[0]
	if e.Msg != "something happened" || e.Level != "info" {
		t.Fatalf("wrong line: %+v", e)
	}
	if e.Attrs["count"] != "7" {
		t.Fatalf("attributes missing: %+v", e.Attrs)
	}
}

// An "agent" attribute makes the line belong to that agent. Without it the
// interface would have to parse text to show a runner's log filtered to one
// start.
func TestAgentAttributeBecomesTheOwner(t *testing.T) {
	control, nodeSide := NewInProc()
	defer control.Close()

	agentID := uuid.New()
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: t.TempDir()}, quietLog())
	t.Cleanup(node.Close)

	node.Log.Info("sandbox started", "agent", agentID.String())
	node.flushLogs(context.Background(), nodeSide)

	batch, _ := decode[LogBatch](receiveType(t, control, TypeLog))
	if batch.Entries[0].AgentID != agentID {
		t.Fatalf("agent not attributed: %+v", batch.Entries[0])
	}
	if _, twice := batch.Entries[0].Attrs["agent"]; twice {
		t.Fatal("agent is in the attributes as well — once is enough")
	}
}

// Debug lines only travel once somebody switches the level. And the switch has
// to WORK: a level that says debug and delivers nothing is the failure that
// stays hidden longest.
func TestLevelSwitchReleasesDebug(t *testing.T) {
	control, nodeSide := NewInProc()
	defer control.Close()

	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: t.TempDir()}, quietLog())
	t.Cleanup(node.Close)

	node.Log.Debug("quiet")
	node.flushLogs(context.Background(), nodeSide)
	still, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if msg, err := control.Receive(still); err == nil {
		t.Fatalf("at info no debug line may go up: %s", msg.Type)
	}

	node.handle(context.Background(), nodeSide, mustEncode(t, TypeSetLogLevel, "1", SetLogLevel{Level: LogLevelDebug}))
	res, err := decode[SetLogLevel](receiveType(t, control, TypeLogLevelResult))
	if err != nil || res.Level != LogLevelDebug {
		t.Fatalf("level not confirmed: %+v %v", res, err)
	}

	node.Log.Debug("loud now")
	node.flushLogs(context.Background(), nodeSide)
	batch, _ := decode[LogBatch](receiveType(t, control, TypeLog))
	var loud bool
	for _, e := range batch.Entries {
		if e.Msg == "loud now" && e.Level == "debug" {
			loud = true
		}
	}
	if !loud {
		t.Fatalf("the debug line is missing after the switch: %+v", batch.Entries)
	}
}

// A typo must not silence a host. An unknown level is refused, and the answer
// names the level that APPLIES — not the one that was asked for.
func TestUnknownLevelChangesNothing(t *testing.T) {
	control, nodeSide := NewInProc()
	defer control.Close()
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: t.TempDir()}, quietLog())
	t.Cleanup(node.Close)

	node.handle(context.Background(), nodeSide, mustEncode(t, TypeSetLogLevel, "1", SetLogLevel{Level: "warn"}))
	res, _ := decode[SetLogLevel](receiveType(t, control, TypeLogLevelResult))
	if res.Level != LogLevelInfo {
		t.Fatalf("a typo moved the level: %q", res.Level)
	}
}

// The ring overflows instead of filling the host — and it says how much was
// lost. A silence here reads exactly like a quiet host.
func TestRingDropsOldestAndCounts(t *testing.T) {
	ring := newLogRing(slog.LevelInfo)
	for i := 0; i < logRingCap+5; i++ {
		ring.add(LogEntry{Time: time.Now(), Level: "info", Msg: "x"})
	}
	entries, dropped := ring.take(logRingCap + 10)
	if dropped != 5 {
		t.Fatalf("expected 5 dropped lines, got %d", dropped)
	}
	if len(entries) != logRingCap {
		t.Fatalf("the ring holds %d lines instead of %d", len(entries), logRingCap)
	}
}

// --- helpers ---

func receiveType(t *testing.T, tr Transport, want string) Message {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for {
		msg, err := tr.Receive(ctx)
		if err != nil {
			t.Fatalf("no message of type %q: %v", want, err)
		}
		if msg.Type == want {
			return msg
		}
	}
}

func mustEncode(t *testing.T, msgType, id string, payload any) Message {
	t.Helper()
	msg, err := encode(msgType, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// A host carries what it says it will carry — and it says so itself, from its
// own configuration, because how much a machine can take is a fact about the
// machine.
//
// The limit is enforced on the host and not only in the scheduler. That is not
// belt and braces: the scheduler works from a count it keeps itself, and
// between its decision and the start a second one can arrive. Only the host
// knows what is actually running on it, and only the host falls over when the
// number is wrong.
func TestTheHostRefusesBeyondItsLimit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.MaxSandboxes = 1
	t.Cleanup(node.Close)

	control, nodeSide := NewInProc()
	defer control.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	go node.Run(ctx, nodeSide)
	receiveType(t, control, TypeRegistered)

	// The first one is taken.
	first := uuid.New()
	send(t, ctx, control, TypeStartSandbox, "1", StartSandbox{AgentID: first, OrgID: orgID})
	if res, _ := decode[SandboxResult](receiveType(t, control, TypeSandboxStarted)); res.AgentID != first {
		t.Fatalf("the first sandbox did not start: %+v", res)
	}

	// The second is refused, and the answer says why rather than timing out.
	send(t, ctx, control, TypeStartSandbox, "2", StartSandbox{AgentID: uuid.New(), OrgID: orgID})
	res, err := decode[SandboxResult](receiveType(t, control, TypeSandboxFailed))
	if err != nil {
		t.Fatalf("no answer to the second start: %v", err)
	}
	if !strings.Contains(res.Err, "limit") {
		t.Fatalf("the refusal does not say what happened: %q", res.Err)
	}

	// Restarting an agent that already runs here REPLACES its sandbox rather
	// than adding one. Refusing that would leave a full host unable to restart
	// its own agents — which is exactly when one usually needs restarting.
	send(t, ctx, control, TypeStartSandbox, "3", StartSandbox{AgentID: first, OrgID: orgID})
	if res, _ := decode[SandboxResult](receiveType(t, control, TypeSandboxStarted)); res.AgentID != first {
		t.Fatalf("a running agent may be restarted on a full host: %+v", res)
	}
}

func send(t *testing.T, ctx context.Context, tr Transport, msgType, id string, payload any) {
	t.Helper()
	if err := tr.Send(ctx, mustEncode(t, msgType, id, payload)); err != nil {
		t.Fatalf("send %s: %v", msgType, err)
	}
}
