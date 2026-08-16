package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
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

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if s.Runners == nil {
		writeJSON(w, http.StatusOK, []runnerstore.Snapshot{})
		return
	}
	list, err := s.Runners.ListSnapshots(r.Context(), id, 100)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []runnerstore.Snapshot{}
	}
	writeJSON(w, http.StatusOK, list)
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

// handleRestoreSnapshot makes an earlier state the current one — the rollback
// that falls out of the construction anyway.
//
// A modifying action on somebody else's work, so it gets the same treatment as
// other interventions: the appropriate role, an audit event, and only while the
// agent is NOT running — otherwise the running sandbox writes into a home that
// changes underneath it.
func (s *Server) handleRestoreSnapshot(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Snapshot string `json:"snapshot"`
	}
	if err := readJSON(r, &in); err != nil || in.Snapshot == "" {
		writeErr(w, http.StatusBadRequest, "snapshot missing")
		return
	}
	if s.RunnerPool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no runner pool")
		return
	}
	p := principalFrom(r)

	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if agent.Status != agents.StatusSleeping {
		writeErr(w, http.StatusConflict,
			"the agent is working — a home may only be restored while it is asleep")
		return
	}

	snapID, err := uuid.Parse(in.Snapshot)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "snapshot: no valid ID")
		return
	}
	snap, err := s.Runners.GetSnapshot(r.Context(), p.OrgID, snapID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if snap.AgentID != id {
		writeErr(w, http.StatusNotFound, "snapshot not found")
		return
	}
	if err := s.RunnerPool.Restore(r.Context(), id, p.OrgID, snap.ManifestHash); err != nil {
		if errors.Is(err, runner.ErrNoRunner) {
			writeErr(w, http.StatusConflict,
				"the runner holding this home is not connected — nothing can be restored right now")
			return
		}
		mapErr(w, err)
		return
	}
	s.recordFileOp(r, id, "home_restore", snap.CreatedAt.Format(time.RFC3339), snap.TotalSize)
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
	Snapshots    int   `json:"snapshots"`
	Agents       int   `json:"agents"`
	KeepPerAgent int   `json:"keep_per_agent"`
	MaxAgeDays   int   `json:"max_age_days"`
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
	if ret, err := s.Runners.RetentionFor(r.Context(), p.OrgID); err == nil {
		view.KeepPerAgent = ret.KeepPerAgent
		view.MaxAgeDays = int(ret.MaxAge / (24 * time.Hour))
	}
	_ = s.Pool.QueryRow(r.Context(),
		`SELECT count(*), count(DISTINCT agent_id) FROM home_snapshots WHERE org_id = $1`, p.OrgID).
		Scan(&view.Snapshots, &view.Agents)
	// The CURRENT homes, not the sum over all snapshots: an agent's ten
	// snapshots are ten versions of one home, and adding them up would produce
	// a figure that says nothing about anything.
	_ = s.Pool.QueryRow(r.Context(), `
		SELECT COALESCE(SUM(total_size), 0) FROM (
			SELECT DISTINCT ON (agent_id) total_size
			  FROM home_snapshots WHERE org_id = $1
			 ORDER BY agent_id, created_at DESC
		) aktuell`, p.OrgID).Scan(&view.LogicalBytes)
	view.Bytes = s.storeSize(r.Context(), p.OrgID)
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleSetRetention(w http.ResponseWriter, r *http.Request) {
	var in struct {
		KeepPerAgent int `json:"keep_per_agent"`
		MaxAgeDays   int `json:"max_age_days"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "keep_per_agent/max_age_days missing")
		return
	}
	p := principalFrom(r)
	if err := s.Runners.SetRetention(r.Context(), p.OrgID, in.KeepPerAgent, in.MaxAgeDays); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
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
