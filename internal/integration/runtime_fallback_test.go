package integration

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/runtimes"
)

const (
	testLimitPrimaryEngine  = "test-limit-primary"
	testLimitFallbackEngine = "test-limit-fallback"
)

var (
	testLimitPrimaryRuns  atomic.Int32
	testLimitFallbackRuns atomic.Int32
)

type testLimitPrimaryRuntime struct{}

func (testLimitPrimaryRuntime) Name() string { return testLimitPrimaryEngine }

func (testLimitPrimaryRuntime) Run(_ context.Context, spec daemon.RunSpec,
	_ func(string, json.RawMessage)) (daemon.RunResult, error) {
	testLimitPrimaryRuns.Add(1)
	if strings.Contains(spec.Body, "ordinary failure") {
		return daemon.RunResult{Status: "failed", Error: "ordinary failure"}, nil
	}
	if strings.Contains(spec.Body, "provider 429") {
		return daemon.RunResult{Status: "failed", Error: "API error 429: rate limit exceeded"}, nil
	}
	return daemon.RunResult{
		Status: "failed",
		Error:  "You've hit your weekly limit · resets Aug 17, 9pm (UTC)",
	}, nil
}

type testLimitFallbackRuntime struct{}

func (testLimitFallbackRuntime) Name() string { return testLimitFallbackEngine }

func (testLimitFallbackRuntime) Run(_ context.Context, _ daemon.RunSpec,
	_ func(string, json.RawMessage)) (daemon.RunResult, error) {
	testLimitFallbackRuns.Add(1)
	return daemon.RunResult{Status: "done", Result: "completed on fallback"}, nil
}

func init() {
	daemon.RegisterRuntime(daemon.RuntimeDescriptor{
		Name:  testLimitPrimaryEngine,
		Label: "Test limit primary",
		Credentials: []daemon.RuntimeCredential{
			{Kind: daemon.CredAPIKey, Secret: "test_primary_stale", EnvVar: "TEST_PRIMARY_STALE"},
			{Kind: daemon.CredSubscription, Secret: "test_primary_seat", EnvVar: "TEST_PRIMARY_SEAT"},
		},
		Capabilities: daemon.RuntimeCapabilities{Resume: true},
		New:          func() daemon.Runtime { return testLimitPrimaryRuntime{} },
	})
	daemon.RegisterRuntime(daemon.RuntimeDescriptor{
		Name:  testLimitFallbackEngine,
		Label: "Test limit fallback",
		Credentials: []daemon.RuntimeCredential{
			{Kind: daemon.CredAPIKey, Secret: "test_fallback_key", EnvVar: "TEST_FALLBACK_KEY"},
		},
		Capabilities: daemon.RuntimeCapabilities{Resume: true},
		New:          func() daemon.Runtime { return testLimitFallbackRuntime{} },
	})
}

// TestProviderLimitRetriesTaskOnConfiguredRuntimeFallback proves the complete
// observed failure mode: the primary seat discovers its weekly limit, a stale
// API-key reference must not block selection, and the same task completes on
// the configured fallback engine instead of remaining failed.
func TestProviderLimitRetriesTaskOnConfiguredRuntimeFallback(t *testing.T) {
	testRuntimeFallbackRecovery(t, "do the work", runtimes.ReasonLimit)
}

// TestProviderRateLimitRetriesTaskOnConfiguredRuntimeFallback protects the
// generic API-capacity case as well as subscription-window exhaustion. A 429
// must not become a terminal task failure merely because it has a shorter
// cooldown than a weekly seat limit.
func TestProviderRateLimitRetriesTaskOnConfiguredRuntimeFallback(t *testing.T) {
	testRuntimeFallbackRecovery(t, "provider 429", runtimes.ReasonLimit)
}

func testRuntimeFallbackRecovery(t *testing.T, body, wantReason string) {
	t.Helper()
	testLimitPrimaryRuns.Store(0)
	testLimitFallbackRuns.Store(0)
	s := newStack(t)
	ctx := context.Background()

	primary, err := s.runtimes.Create(ctx, s.orgID, testLimitPrimaryEngine, "Primary", "")
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := s.runtimes.Create(ctx, s.orgID, testLimitFallbackEngine, "Fallback", "")
	if err != nil {
		t.Fatal(err)
	}
	// This reference deliberately outlives its secret, exactly as in the live
	// database. The paid-for subscription still wins the first selection.
	s.seat(t, primary.ID, daemon.CredAPIKey,
		"test_primary_stale", "deleted-primary-key", "stale")
	s.seat(t, primary.ID, daemon.CredSubscription,
		"test_primary_seat", "primary-seat", "seat")
	if err := s.secrets.Delete(ctx, s.orgID, "test_primary_stale"); err != nil {
		t.Fatal(err)
	}
	s.seat(t, fallback.ID, daemon.CredAPIKey,
		"test_fallback_key", "fallback-key", "fallback")
	if err := s.runtimes.SetFallback(ctx, s.orgID, primary.ID, &fallback.ID); err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "fallback-agent", "Fallback Agent",
		testLimitPrimaryEngine, &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md": "# Fallback Agent\n\nFinish the task.",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.Assign(ctx, s.orgID, agent.ID, primary.ID); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID,
		"Continue after provider limit", body, "manual", 4)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "task to reach a terminal state", 5*time.Second, func() bool {
		state := s.taskState(task.ID)
		return state == backlog.StateDone || state == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != backlog.StateDone || got.Result == nil || *got.Result != "completed on fallback" {
		t.Fatalf("task did not recover on fallback: state=%s result=%v error=%v",
			got.State, got.Result, got.Error)
	}
	if testLimitPrimaryRuns.Load() != 1 || testLimitFallbackRuns.Load() != 1 {
		t.Fatalf("runs primary=%d fallback=%d, want exactly one each",
			testLimitPrimaryRuns.Load(), testLimitFallbackRuns.Load())
	}
	primaryState, err := s.runtimes.Get(ctx, s.orgID, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(primaryState.Credentials) != 2 ||
		primaryState.Credentials[1].CooldownReason != wantReason {
		t.Fatalf("primary limit was not persisted: %+v", primaryState.Credentials)
	}
}

// TestOrdinaryRuntimeFailureRemainsTerminal protects the boundary: runtime
// fallback is capacity recovery, not a general retry mechanism.
func TestOrdinaryRuntimeFailureRemainsTerminal(t *testing.T) {
	testLimitPrimaryRuns.Store(0)
	testLimitFallbackRuns.Store(0)
	s := newStack(t)
	ctx := context.Background()

	primary, _ := s.runtimes.Create(ctx, s.orgID, testLimitPrimaryEngine, "Primary", "")
	fallback, _ := s.runtimes.Create(ctx, s.orgID, testLimitFallbackEngine, "Fallback", "")
	s.seat(t, primary.ID, daemon.CredSubscription,
		"test_primary_seat", "primary-seat", "seat")
	s.seat(t, fallback.ID, daemon.CredAPIKey,
		"test_fallback_key", "fallback-key", "fallback")
	if err := s.runtimes.SetFallback(ctx, s.orgID, primary.ID, &fallback.ID); err != nil {
		t.Fatal(err)
	}
	agent, err := s.registry.Create(ctx, s.orgID, "ordinary-failure-agent", "Ordinary Failure Agent",
		testLimitPrimaryEngine, &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID,
		map[string]string{"SOUL.md": "# Agent"}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.Assign(ctx, s.orgID, agent.ID, primary.ID); err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID,
		"Ordinary failure", "ordinary failure", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "ordinary task to fail", 5*time.Second,
		func() bool { return s.taskState(task.ID) == backlog.StateFailed })
	if testLimitPrimaryRuns.Load() != 1 || testLimitFallbackRuns.Load() != 0 {
		t.Fatalf("ordinary failure retried: primary=%d fallback=%d",
			testLimitPrimaryRuns.Load(), testLimitFallbackRuns.Load())
	}
}
