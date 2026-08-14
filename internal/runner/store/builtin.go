package store

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// BuiltinTokens hands out the built-in runner of an organisation together with
// a token for this process's lifetime.
//
// The token is rolled once per control-plane start and lives only in memory;
// the database keeps its hash. That is deliberate: nobody outside this process
// needs it beyond the runtime of the components it starts, and a long-lived
// secret at rest would have to be protected without buying anything. The price
// is one restart of the egress proxy per control-plane start, and the proxy
// holds no state (see DockerProvider.ensureEgressProxy).
//
// Organisations created while the process runs get theirs on first use — hence
// a cache and not a list read once at startup.
type BuiltinTokens struct {
	store *Store

	mu    sync.Mutex
	byOrg map[uuid.UUID]issued
}

type issued struct {
	id    uuid.UUID
	token string
}

func NewBuiltinTokens(store *Store) *BuiltinTokens {
	return &BuiltinTokens{store: store, byOrg: map[uuid.UUID]issued{}}
}

// For returns the organisation's built-in runner and its token, creating the
// runner and rolling the token on first use.
func (b *BuiltinTokens) For(ctx context.Context, orgID uuid.UUID) (uuid.UUID, string, error) {
	b.mu.Lock()
	got, ok := b.byOrg[orgID]
	b.mu.Unlock()
	if ok {
		return got.id, got.token, nil
	}

	// Outside the lock: this talks to the database, and holding a mutex across
	// it would serialise every organisation behind the slowest one. Two
	// concurrent callers may roll two tokens; the loser's is overwritten below,
	// which costs a wasted token and nothing else.
	rn, err := b.store.EnsureBuiltin(ctx, orgID)
	if err != nil {
		return uuid.Nil, "", err
	}
	token, err := NewToken()
	if err != nil {
		return uuid.Nil, "", err
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if got, ok := b.byOrg[orgID]; ok {
		return got.id, got.token, nil // someone was faster; their token is the one that counts
	}
	if err := b.store.SetTokenHash(ctx, rn.ID, HashToken(token)); err != nil {
		return uuid.Nil, "", err
	}
	b.byOrg[orgID] = issued{id: rn.ID, token: token}
	return rn.ID, token, nil
}
