package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/daemon"
)

// Reported utilisation, asked centrally and cached.
//
// The provider's usage endpoint has a rate limit of its own, and asking once
// per run across a fleet would hit it — after which the engine serves figures
// up to an hour old and eventually none at all. Three agents on one seat get
// the same answer anyway, so the question is asked PER CREDENTIAL and the
// answer is held briefly (spec/07, D13).
//
// The cache is deliberately short. The figure exists to say "this seat is
// nearly full"; a few minutes of staleness does not change that answer, and a
// longer memory would start hiding a seat that has just run dry.
const usageCacheTTL = 5 * time.Minute

// usageAsk is how long a run may be held up by the question. It is asked in the
// background and never in the critical path, so this only bounds the goroutine.
const usageAsk = 60 * time.Second

type usageEntry struct {
	usage daemon.Usage
	at    time.Time
	// unsupported remembers an engine that cannot answer, so we stop asking
	// rather than paying for the round trip on every wake.
	unsupported bool
}

type usageCache struct {
	mu sync.Mutex
	by map[usageKey]usageEntry
}

type usageKey struct {
	runtime uuid.UUID
	ord     int
}

func newUsageCache() *usageCache { return &usageCache{by: map[usageKey]usageEntry{}} }

func (c *usageCache) get(k usageKey) (usageEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.by[k]
	if !ok {
		return usageEntry{}, false
	}
	// An engine that cannot answer stays remembered without an expiry — that
	// property does not change between two runs.
	if e.unsupported {
		return e, true
	}
	return e, time.Since(e.at) < usageCacheTTL
}

func (c *usageCache) put(k usageKey, e usageEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.by[k] = e
}

// Usage returns the engine's own figure for a credential, if it has one and it
// is still fresh. The second return value says whether anything is known at
// all; the caller falls back to the platform's own estimate.
func (o *Orchestrator) Usage(runtimeID uuid.UUID, ord int) (daemon.Usage, bool) {
	if o.usage == nil {
		return daemon.Usage{}, false
	}
	e, fresh := o.usage.get(usageKey{runtimeID, ord})
	if !fresh || e.unsupported || !e.usage.Reported() {
		return daemon.Usage{}, false
	}
	return e.usage, true
}

// refreshUsage asks the daemon of a running agent what its credential has
// consumed. Fire and forget: the answer arrives as an ordinary protocol message
// and is filed by handleDaemonMessage, so nothing here waits.
//
// Asked AFTER a waking phase rather than before one: at that point a link
// exists anyway, the run is over, and a question that goes wrong costs nothing.
// Asking beforehand would put a round trip in front of every wake for a figure
// no run needs in order to start.
func (o *Orchestrator) refreshUsage(ctx context.Context, link DaemonLink, runtimeID uuid.UUID, ord int) {
	if o.usage == nil || runtimeID == uuid.Nil || ord < 0 {
		return
	}
	if e, fresh := o.usage.get(usageKey{runtimeID, ord}); fresh && (e.unsupported || e.usage.Reported()) {
		return // still good, or an engine that cannot answer anyway
	}
	askCtx, cancel := context.WithTimeout(ctx, usageAsk)
	defer cancel()
	_ = o.sendMsg(askCtx, link, daemon.TypeRequestUsage,
		daemon.RequestUsage{RequestID: uuid.NewString()})
}

// noteUsage files an answer against the credential the session is running on.
func (o *Orchestrator) noteUsage(rep daemon.UsageReport, runtimeID uuid.UUID, ord int) {
	if o.usage == nil || runtimeID == uuid.Nil || ord < 0 {
		return
	}
	o.usage.put(usageKey{runtimeID, ord},
		usageEntry{usage: rep.Usage, at: time.Now(), unsupported: !rep.Supported})
	if rep.Supported {
		o.Log.Info("utilisation reported", "runtime", runtimeID, "ord", ord,
			"window_pct", rep.Usage.WindowPercent, "week_pct", rep.Usage.WeekPercent,
			"stale", rep.Usage.Stale)
	}
}

// reportedUtilisation is the capacity layer's view of what the provider said —
// the share of the current window, if the engine could ask and the answer is
// recent enough to act on.
//
// A STALE figure is withheld rather than passed on with a flag. It is up to an
// hour old (the engine serves it from its own cache when the provider's
// endpoint is rate limited), and the decision it would feed is "skip this
// credential" — taking a seat out of play on an hour-old number would cost
// exactly the capacity this whole mechanism exists to use up.
func (o *Orchestrator) reportedUtilisation(runtimeID uuid.UUID, ord int) (float64, bool) {
	u, ok := o.Usage(runtimeID, ord)
	if !ok || u.Stale || u.WindowPercent < 0 {
		return 0, false
	}
	return u.WindowPercent, true
}
