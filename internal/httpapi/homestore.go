package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/runner"
	runnerstore "covey/internal/runner/store"
)

// The home store as the interface sees it (spec/16, "Interface"). A store that
// grows quietly in the background and whose content nobody can see is an
// operational risk — you notice it when the disk is full. So both belong here
// and not only in an environment variable.

// AgentHomeView is what the agent page shows.
type AgentHomeView struct {
	// Enabled=false: the home store is switched off (COVEY_HOME_STORE=false).
	// Then there are no snapshots, no rollback, and a lost home is
	// unrecoverable — which is worth saying rather than showing empty figures.
	Enabled bool `json:"enabled"`
	runnerstore.HomeSummary
	// TotalBytes is the home as the agent sees it, ExclusiveBytes what only it
	// holds. The difference is the actual statement.
	TotalBytes     int64                `json:"total_bytes"`
	ExclusiveBytes int64                `json:"exclusive_bytes"`
	TopDirs        []homestore.DirUsage `json:"top_dirs,omitempty"`
}

func (s *Server) handleAgentHome(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	view := AgentHomeView{Enabled: s.Blobs != nil && s.Runners != nil}
	if !view.Enabled {
		writeJSON(w, http.StatusOK, view)
		return
	}

	summary, err := s.Runners.HomeSummaryFor(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	view.HomeSummary = summary
	if summary.Latest == nil {
		writeJSON(w, http.StatusOK, view)
		return
	}

	m, err := homestore.Load(r.Context(), s.Blobs, p.OrgID, summary.Latest.ManifestHash)
	if err != nil {
		// The row is there, the manifest is not — a store that has been swept
		// too far. Said plainly instead of as a page of zeroes.
		s.Log.Warn("manifest of the latest snapshot not readable", "agent", id, "err", err)
		writeJSON(w, http.StatusOK, view)
		return
	}
	view.TotalBytes = m.TotalSize()
	view.TopDirs = m.TopDirs(8)
	view.ExclusiveBytes = m.ExclusiveBytes(s.sharedBlocks(r.Context(), p.OrgID, id))
	writeJSON(w, http.StatusOK, view)
}

// sharedBlocks are the blocks that OTHER agents of the organisation also hold.
// Everything outside it belongs to this home alone — the figure that says
// whether losing it costs time or work.
func (s *Server) sharedBlocks(ctx context.Context, orgID, agentID uuid.UUID) map[string]bool {
	list, err := s.Registry.List(ctx, orgID)
	if err != nil {
		return nil
	}
	shared := map[string]bool{}
	for _, a := range list {
		if a.ID == agentID {
			continue
		}
		snap, err := s.Runners.LatestSnapshot(ctx, a.ID)
		if err != nil || snap.ManifestHash == "" {
			continue
		}
		m, err := homestore.Load(ctx, s.Blobs, orgID, snap.ManifestHash)
		if err != nil {
			continue
		}
		for b := range m.BlockSet() {
			shared[b] = true
		}
	}
	return shared
}

// handleBackupNow forces a sync — before a maintenance window, or simply
// because somebody wants the current state safe.
func (s *Server) handleBackupNow(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if s.RunnerPool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no runner pool")
		return
	}
	p := principalFrom(r)
	if err := s.RunnerPool.SyncNow(r.Context(), id, p.OrgID, "manual"); err != nil {
		if errors.Is(err, runner.ErrNoRunner) {
			writeErr(w, http.StatusConflict,
				"the runner holding this home is not connected — nothing can be backed up right now")
			return
		}
		mapErr(w, err)
		return
	}
	s.recordFileOp(r, id, "home_backup", "", 0)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- The store as a whole ---

// StoreView is the fill level for the dashboard: total size, growth, and a
// warning before the disk runs short — not after.
type StoreView struct {
	Enabled bool `json:"enabled"`
	// Bytes is what lies on the disk, LogicalBytes what the homes weigh as
	// their agents see them. The pair is the whole explanation of this
	// construction in two numbers: the second is regularly a multiple of the
	// first, because the toolchain caches are byte-for-byte identical on every
	// developer home and are therefore stored once.
	//
	// Without the comparison the store is a directory that grows for reasons
	// nobody can see. With it, one line says what it is doing.
	Bytes        int64 `json:"bytes"`
	LogicalBytes int64 `json:"logical_bytes"`
	// Agents is how many of them have a home in the store. There is no snapshot
	// count beside it any more: with one state per agent the two would be the
	// same number printed twice.
	Agents int `json:"agents"`
}

// storeSizeCache: walking the block directory is a disk pass, and the
// dashboard asks on every visit. A figure a few minutes old is the right
// trade — it moves in gigabytes over hours, not in bytes over seconds.
type storeSizeCache struct {
	mu       sync.Mutex
	byOrg    map[uuid.UUID]int64
	measured map[uuid.UUID]time.Time
}

const storeSizeTTL = 5 * time.Minute

func (s *Server) storeSize(ctx context.Context, orgID uuid.UUID) int64 {
	sizer, ok := s.Blobs.(interface {
		Size(context.Context, uuid.UUID) (int64, error)
	})
	if !ok {
		return 0
	}
	s.storeSizes.mu.Lock()
	if s.storeSizes.byOrg == nil {
		s.storeSizes.byOrg = map[uuid.UUID]int64{}
		s.storeSizes.measured = map[uuid.UUID]time.Time{}
	}
	size, at := s.storeSizes.byOrg[orgID], s.storeSizes.measured[orgID]
	s.storeSizes.mu.Unlock()
	if time.Since(at) < storeSizeTTL {
		return size
	}

	size, err := sizer.Size(ctx, orgID)
	if err != nil {
		return 0
	}
	s.storeSizes.mu.Lock()
	s.storeSizes.byOrg[orgID] = size
	s.storeSizes.measured[orgID] = time.Now()
	s.storeSizes.mu.Unlock()
	return size
}

func (s *Server) handleGetStore(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	view := StoreView{Enabled: s.Blobs != nil && s.Runners != nil}
	if !view.Enabled {
		writeJSON(w, http.StatusOK, view)
		return
	}
	_ = s.Pool.QueryRow(r.Context(), `
		SELECT count(*), COALESCE(SUM(total_size), 0)
		  FROM home_snapshots WHERE org_id = $1`, p.OrgID).
		Scan(&view.Agents, &view.LogicalBytes)
	view.Bytes = s.storeSize(r.Context(), p.OrgID)
	writeJSON(w, http.StatusOK, view)
}

// The cleanup itself lives in the runner store (CleanupOrg), because the CLI
// and the periodic pass need exactly the same steps in exactly the same order.
// A second copy here is how the button and the timer start to disagree about
// what "cleaned up" means.
func (s *Server) handleCleanupStore(w http.ResponseWriter, r *http.Request) {
	if s.Blobs == nil || s.Runners == nil {
		writeErr(w, http.StatusServiceUnavailable, "the home store is switched off")
		return
	}
	p := principalFrom(r)
	preview := r.URL.Query().Get("preview") != "false"

	out, err := s.Runners.CleanupOrg(r.Context(), s.Blobs, p.OrgID, preview)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
