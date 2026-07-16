package egress

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// DBResolver löst die per-Agent-Allowlist aus dem Store auf (mit kurzem Cache),
// validiert das per-Sandbox-Token und schreibt das Entscheidungs-Log asynchron.
// Er bedient sowohl den kooperativen Proxy (im Control-Plane-Prozess) als auch
// den standalone Proxy-Container (network-Modus) — beide sprechen dieselbe DB.
type DBResolver struct {
	store    *Store
	defaults []string // immer erlaubt (z. B. api.anthropic.com)
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
	expires   time.Time
}

type logItem struct {
	agent   uuid.UUID
	host    string
	method  string
	allowed bool
}

// NewDBResolver startet den Resolver samt Log-Writer, gebunden an ctx.
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

// Resolve validiert (agentID, token) und liefert die effektive Allowlist des
// Agenten (Defaults + Templates + eigene Hosts). Fail-closed bei jedem Fehler.
func (r *DBResolver) Resolve(ctx context.Context, agentID, token string) (*Allowlist, uuid.UUID, bool) {
	id, err := uuid.Parse(agentID)
	if err != nil {
		return nil, uuid.Nil, false
	}

	r.mu.Lock()
	entry, ok := r.cache[id]
	r.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		entry, err = r.load(ctx, id)
		if err != nil {
			r.log.Warn("egress-resolver: laden fehlgeschlagen", "agent", id, "err", err)
			return nil, uuid.Nil, false
		}
		r.mu.Lock()
		r.cache[id] = entry
		r.mu.Unlock()
	}

	if entry.tokenHash == "" || HashToken(token) != entry.tokenHash {
		return nil, uuid.Nil, false
	}
	return entry.allow, id, true
}

func (r *DBResolver) load(ctx context.Context, id uuid.UUID) (cachedEntry, error) {
	hash, err := r.store.AgentTokenHash(ctx, id)
	if err != nil {
		// Kein Token hinterlegt → kein gültiger Zugang (fail-closed). Trotzdem
		// mit leerem Hash cachen, damit nicht jede Anfrage die DB trifft.
		return cachedEntry{allow: NewAllowlist(r.defaults), tokenHash: "", expires: time.Now().Add(r.ttl)}, nil
	}
	patterns, err := r.store.EffectiveAllowlist(ctx, id)
	if err != nil {
		return cachedEntry{}, err
	}
	all := append(append([]string{}, r.defaults...), patterns...)
	return cachedEntry{allow: NewAllowlist(all), tokenHash: hash, expires: time.Now().Add(r.ttl)}, nil
}

// Log reiht eine Entscheidung nicht-blockierend ein; bei vollem Puffer wird
// verworfen (gezählt), damit der Proxy nie an der DB hängt.
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
				r.log.Warn("egress-log schreiben fehlgeschlagen", "err", err)
			}
			cancel()
		}
	}
}
