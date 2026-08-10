package egress

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Decision is a single proxy decision — the unit of the decision log. It leaves
// the process in this shape too: the standalone proxy posts it to the control
// plane instead of writing it to the database itself (spec/16, trust boundary).
type Decision struct {
	AgentID uuid.UUID `json:"agent_id"`
	Host    string    `json:"host"`
	Method  string    `json:"method"`
	Allowed bool      `json:"allowed"`
}

// loadFunc fetches an agent's effective allowlist and the hash of its
// per-sandbox token. An empty hash with a nil error means "no valid token
// stored" — a fail-closed state that is worth caching, in contrast to an error.
type loadFunc func(ctx context.Context, agentID uuid.UUID) (patterns []string, tokenHash string, err error)

// sinkFunc takes a batch of decisions.
type sinkFunc func(ctx context.Context, batch []Decision) error

// resolver is what both resolvers have in common: the short cache in front of
// the source, the token check against it, and the decision log that never
// blocks the proxy. What differs is only where the two come from — the database
// (in the control plane's process) or the control-plane API (in the proxy
// container). That is the same distinction the runner draws between the
// in-process and the remote transport, and it is drawn here for the same
// reason: one implementation of the logic, two ways to reach the data.
type resolver struct {
	load     loadFunc
	sink     sinkFunc
	defaults []string // always-allowed additions from the environment (COVEY_EGRESS_ALLOW, e.g. host.docker.internal)
	ttl      time.Duration
	log      *slog.Logger

	mu    sync.Mutex
	cache map[uuid.UUID]cachedEntry

	logCh   chan Decision
	dropped int64
}

type cachedEntry struct {
	allow     *Allowlist
	tokenHash string
	loaded    time.Time
	expires   time.Time
}

// maxBatch bounds a single write of the decision log. Without it a proxy that
// has been logging into a broken sink for minutes would deliver its whole
// backlog in one request the moment the sink comes back.
const maxBatch = 100

func newResolver(ctx context.Context, load loadFunc, sink sinkFunc, defaults []string, ttl time.Duration, log *slog.Logger) *resolver {
	if log == nil {
		log = slog.Default()
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	r := &resolver{
		load:     load,
		sink:     sink,
		defaults: defaults,
		ttl:      ttl,
		log:      log,
		cache:    map[uuid.UUID]cachedEntry{},
		logCh:    make(chan Decision, 2048),
	}
	go r.writeLoop(ctx)
	return r
}

// Resolve validates (agentID, token) and returns the agent's effective
// allowlist (defaults + templates + own hosts). Fail-closed on any error.
func (r *resolver) Resolve(ctx context.Context, agentID, token string) (*Allowlist, uuid.UUID, bool) {
	id, err := uuid.Parse(agentID)
	if err != nil {
		return nil, uuid.Nil, false
	}

	r.mu.Lock()
	entry, ok := r.cache[id]
	r.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		if entry, err = r.reload(ctx, id); err != nil {
			return nil, uuid.Nil, false
		}
	}

	if entry.tokenHash == "" || HashToken(token) != entry.tokenHash {
		// The token rotates when the sandbox wakes — a cached, stale hash must
		// not lock the fresh sandbox out with 407 until the TTL expires. Reload
		// once and check again; the minimum freshness bounds how often wrong
		// tokens can trigger a round-trip to the source.
		if time.Since(entry.loaded) < 2*time.Second {
			return nil, uuid.Nil, false
		}
		if entry, err = r.reload(ctx, id); err != nil {
			return nil, uuid.Nil, false
		}
		if entry.tokenHash == "" || HashToken(token) != entry.tokenHash {
			return nil, uuid.Nil, false
		}
	}
	return entry.allow, id, true
}

// reload loads an agent's cache entry freshly from the source and replaces it
// in the cache. Errors are logged and propagated (fail-closed, without caching
// the failure state).
func (r *resolver) reload(ctx context.Context, id uuid.UUID) (cachedEntry, error) {
	now := time.Now()
	patterns, tokenHash, err := r.load(ctx, id)
	if err != nil {
		r.log.Warn("egress resolver: load failed", "agent", id, "err", err)
		return cachedEntry{}, err
	}
	all := append(append([]string{}, r.defaults...), patterns...)
	entry := cachedEntry{allow: NewAllowlist(all), tokenHash: tokenHash, loaded: now, expires: now.Add(r.ttl)}
	r.mu.Lock()
	r.cache[id] = entry
	r.mu.Unlock()
	return entry, nil
}

// Log queues a decision without blocking; if the buffer is full the entry is
// dropped (and counted) so that the proxy never hangs on the log.
func (r *resolver) Log(agent uuid.UUID, host, method string, allowed bool) {
	select {
	case r.logCh <- Decision{AgentID: agent, Host: host, Method: method, Allowed: allowed}:
	default:
		r.mu.Lock()
		r.dropped++
		r.mu.Unlock()
	}
}

func (r *resolver) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case first := <-r.logCh:
			batch := append(make([]Decision, 0, maxBatch), first)
			// Whatever is already queued travels along — under load that turns a
			// request per decision into one per batch.
			for len(batch) < maxBatch {
				select {
				case next := <-r.logCh:
					batch = append(batch, next)
					continue
				default:
				}
				break
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := r.sink(wctx, batch); err != nil {
				r.log.Warn("writing egress log failed", "entries", len(batch), "err", err)
			}
			cancel()
		}
	}
}

// DBResolver reads the allowlist straight from the store. It belongs to the
// cooperative proxy, which runs inside the control plane's process — there the
// database is not a foreign resource but the process's own.
type DBResolver struct {
	*resolver
	store *Store
}

// NewDBResolver starts the resolver together with its log writer, bound to ctx.
func NewDBResolver(ctx context.Context, store *Store, defaults []string, ttl time.Duration, log *slog.Logger) *DBResolver {
	d := &DBResolver{store: store}
	d.resolver = newResolver(ctx, d.load, d.write, defaults, ttl, log)
	return d
}

func (d *DBResolver) load(ctx context.Context, id uuid.UUID) ([]string, string, error) {
	hash, err := d.store.AgentTokenHash(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		// No token stored → no valid access (fail-closed). Cached with an empty
		// hash anyway so that not every request hits the database.
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	patterns, err := d.store.EffectiveAllowlist(ctx, id)
	if err != nil {
		return nil, "", err
	}
	return patterns, hash, nil
}

func (d *DBResolver) write(ctx context.Context, batch []Decision) error {
	for _, it := range batch {
		if err := d.store.LogDecision(ctx, it.AgentID, it.Host, it.Method, it.Allowed); err != nil {
			return err
		}
	}
	return nil
}
