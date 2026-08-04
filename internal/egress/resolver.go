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

// DBResolver resolves the per-agent allowlist from the store (with a short
// cache), validates the per-sandbox token and writes the decision log
// asynchronously. It serves both the cooperative proxy (inside the control
// plane process) and the standalone proxy container (network mode) — both talk
// to the same database.
type DBResolver struct {
	store    *Store
	defaults []string // always-allowed additions from the environment (COVEY_EGRESS_ALLOW, e.g. host.docker.internal)
	ttl      time.Duration
	log      *slog.Logger

	mu    sync.Mutex
	cache map[uuid.UUID]cachedEntry

	logCh   chan logItem
	dropped int64
}

type cachedEntry struct {
	allow     *Allowlist
	tokenHash string
	loaded    time.Time
	expires   time.Time
}

type logItem struct {
	agent   uuid.UUID
	host    string
	method  string
	allowed bool
}

// NewDBResolver starts the resolver together with its log writer, bound to ctx.
func NewDBResolver(ctx context.Context, store *Store, defaults []string, ttl time.Duration, log *slog.Logger) *DBResolver {
	if log == nil {
		log = slog.Default()
	}
	if ttl <= 0 {
		ttl = 15 * time.Second
	}
	r := &DBResolver{
		store:    store,
		defaults: defaults,
		ttl:      ttl,
		log:      log,
		cache:    map[uuid.UUID]cachedEntry{},
		logCh:    make(chan logItem, 2048),
	}
	go r.writeLoop(ctx)
	return r
}

// Resolve validates (agentID, token) and returns the agent's effective
// allowlist (defaults + templates + own hosts). Fail-closed on any error.
func (r *DBResolver) Resolve(ctx context.Context, agentID, token string) (*Allowlist, uuid.UUID, bool) {
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
		// tokens can trigger a database round-trip.
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

// reload loads an agent's cache entry freshly from the database and replaces
// it in the cache. Errors are logged and propagated (fail-closed, without
// caching the failure state).
func (r *DBResolver) reload(ctx context.Context, id uuid.UUID) (cachedEntry, error) {
	entry, err := r.load(ctx, id)
	if err != nil {
		r.log.Warn("egress resolver: load failed", "agent", id, "err", err)
		return cachedEntry{}, err
	}
	r.mu.Lock()
	r.cache[id] = entry
	r.mu.Unlock()
	return entry, nil
}

func (r *DBResolver) load(ctx context.Context, id uuid.UUID) (cachedEntry, error) {
	now := time.Now()
	hash, err := r.store.AgentTokenHash(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		// No token stored → no valid access (fail-closed). Cache it with an empty
		// hash anyway so that not every request hits the database.
		return cachedEntry{allow: NewAllowlist(r.defaults), tokenHash: "", loaded: now, expires: now.Add(r.ttl)}, nil
	}
	if err != nil {
		return cachedEntry{}, err
	}
	patterns, err := r.store.EffectiveAllowlist(ctx, id)
	if err != nil {
		return cachedEntry{}, err
	}
	all := append(append([]string{}, r.defaults...), patterns...)
	return cachedEntry{allow: NewAllowlist(all), tokenHash: hash, loaded: now, expires: now.Add(r.ttl)}, nil
}

// Log queues a decision without blocking; if the buffer is full the entry is
// dropped (and counted) so that the proxy never hangs on the database.
func (r *DBResolver) Log(agent uuid.UUID, host, method string, allowed bool) {
	select {
	case r.logCh <- logItem{agent: agent, host: host, method: method, allowed: allowed}:
	default:
		r.mu.Lock()
		r.dropped++
		r.mu.Unlock()
	}
}

func (r *DBResolver) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case it := <-r.logCh:
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := r.store.LogDecision(wctx, it.agent, it.host, it.method, it.allowed); err != nil {
				r.log.Warn("writing egress log failed", "err", err)
			}
			cancel()
		}
	}
}
