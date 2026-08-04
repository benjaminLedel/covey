package egress

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrInvalidPattern reports a syntactically unusable allowlist pattern.
var ErrInvalidPattern = errors.New("invalid egress pattern")

// ErrTemplateExists reports a name collision on create/import.
var ErrTemplateExists = errors.New("a template with this name already exists")

// Store keeps the egress configuration (templates, per-agent assignments,
// individual hosts), the per-sandbox tokens and the decision log in Postgres.
// Templates are org-scoped; the effective allowlist is resolved per agent.
type Store struct {
	pool *pgxpool.Pool
}

// ErrNotFound: the object does not exist — or not in this organisation.
var ErrNotFound = errors.New("egress object not found")

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Host is a single allowlist entry (in a template or per agent).
type Host struct {
	ID      uuid.UUID `json:"id"`
	Pattern string    `json:"pattern"`
	Note    string    `json:"note"`
}

// Template is a reusable host set.
type Template struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Hosts       []Host     `json:"hosts"`
	Agents      []AgentRef `json:"agents"`
	CreatedAt   string     `json:"created_at"`
}

// AgentRef names an agent that has a template assigned — for the "used by"
// display in the UI.
type AgentRef struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
}

// LogEntry is a recorded egress decision.
type LogEntry struct {
	ID        int64      `json:"id"`
	AgentID   *uuid.UUID `json:"agent_id,omitempty"`
	AgentSlug string     `json:"agent_slug"`
	Host      string     `json:"host"`
	Method    string     `json:"method"`
	Allowed   bool       `json:"allowed"`
	CreatedAt string     `json:"created_at"`
}

// --- Templates ---

// ListTemplates returns all templates of the organisation together with their hosts.
func (s *Store) ListTemplates(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, created_at FROM egress_templates
		 WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tpls := []Template{}
	byID := map[uuid.UUID]int{}
	for rows.Next() {
		var t Template
		var created time.Time
		if err := rows.Scan(&t.ID, &t.Name, &t.Description, &created); err != nil {
			return nil, err
		}
		t.CreatedAt = created.Format(time.RFC3339)
		t.Hosts = []Host{}
		t.Agents = []AgentRef{}
		byID[t.ID] = len(tpls)
		tpls = append(tpls, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(tpls) == 0 {
		return tpls, nil
	}
	// Load the hosts of all templates in one go and assign them.
	hrows, err := s.pool.Query(ctx,
		`SELECT h.id, h.template_id, h.pattern, h.note FROM egress_template_hosts h
		 JOIN egress_templates t ON t.id=h.template_id
		 WHERE t.org_id=$1 ORDER BY h.pattern`, orgID)
	if err != nil {
		return nil, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var h Host
		var tid uuid.UUID
		if err := hrows.Scan(&h.ID, &tid, &h.Pattern, &h.Note); err != nil {
			return nil, err
		}
		if idx, ok := byID[tid]; ok {
			tpls[idx].Hosts = append(tpls[idx].Hosts, h)
		}
	}
	if err := hrows.Err(); err != nil {
		return nil, err
	}
	// Load the assignments: which agents use which template?
	arows, err := s.pool.Query(ctx,
		`SELECT at.template_id, a.id, a.slug, a.display_name
		 FROM agent_egress_templates at JOIN agents a ON a.id=at.agent_id
		 WHERE a.org_id=$1 ORDER BY a.slug`, orgID)
	if err != nil {
		return nil, err
	}
	defer arows.Close()
	for arows.Next() {
		var ref AgentRef
		var tid uuid.UUID
		if err := arows.Scan(&tid, &ref.ID, &ref.Slug, &ref.DisplayName); err != nil {
			return nil, err
		}
		if idx, ok := byID[tid]; ok {
			tpls[idx].Agents = append(tpls[idx].Agents, ref)
		}
	}
	return tpls, arows.Err()
}

func (s *Store) CreateTemplate(ctx context.Context, orgID uuid.UUID, name, description string) (Template, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Template{}, fmt.Errorf("%w: name missing", ErrInvalidPattern)
	}
	var t Template
	var created time.Time
	err := s.pool.QueryRow(ctx,
		`INSERT INTO egress_templates (org_id, name, description) VALUES ($1,$2,$3)
		 ON CONFLICT (org_id, name) DO NOTHING
		 RETURNING id, name, description, created_at`,
		orgID, name, strings.TrimSpace(description)).Scan(&t.ID, &t.Name, &t.Description, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateExists
	}
	if err != nil {
		return Template{}, err
	}
	t.CreatedAt = created.Format(time.RFC3339)
	t.Hosts = []Host{}
	t.Agents = []AgentRef{}
	return t, nil
}

// ImportBuiltin adopts a catalogue entry as an org-owned template (a copy
// including its hosts). If the name collides with an existing template the
// result is ErrTemplateExists — nothing is created.
func (s *Store) ImportBuiltin(ctx context.Context, orgID uuid.UUID, b BuiltinTemplate) (Template, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Template{}, err
	}
	defer tx.Rollback(ctx)

	var t Template
	var created time.Time
	err = tx.QueryRow(ctx,
		`INSERT INTO egress_templates (org_id, name, description) VALUES ($1,$2,$3)
		 ON CONFLICT (org_id, name) DO NOTHING
		 RETURNING id, name, description, created_at`,
		orgID, b.Name, b.Description).Scan(&t.ID, &t.Name, &t.Description, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Template{}, ErrTemplateExists
	}
	if err != nil {
		return Template{}, err
	}
	t.CreatedAt = created.Format(time.RFC3339)
	t.Hosts = []Host{}
	t.Agents = []AgentRef{}
	for _, h := range b.Hosts {
		var host Host
		err := tx.QueryRow(ctx,
			`INSERT INTO egress_template_hosts (template_id, pattern, note) VALUES ($1,$2,$3)
			 RETURNING id, pattern, note`,
			t.ID, h.Pattern, h.Note).Scan(&host.ID, &host.Pattern, &host.Note)
		if err != nil {
			return Template{}, err
		}
		t.Hosts = append(t.Hosts, host)
	}
	return t, tx.Commit(ctx)
}

func (s *Store) DeleteTemplate(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM egress_templates WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	// See guardrails.Delete: org-scoped, but without this check reaching for
	// another organisation's template reported success as well.
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AddTemplateHost(ctx context.Context, orgID, templateID uuid.UUID, pattern, note string) (Host, error) {
	norm, err := NormalizePattern(pattern)
	if err != nil {
		return Host{}, err
	}
	var h Host
	err = s.pool.QueryRow(ctx,
		`INSERT INTO egress_template_hosts (template_id, pattern, note)
		 SELECT $1,$2,$3 WHERE EXISTS (SELECT 1 FROM egress_templates WHERE id=$1 AND org_id=$4)
		 ON CONFLICT (template_id, pattern) DO UPDATE SET note=EXCLUDED.note
		 RETURNING id, pattern, note`,
		templateID, norm, strings.TrimSpace(note), orgID).Scan(&h.ID, &h.Pattern, &h.Note)
	if err != nil {
		return Host{}, err
	}
	return h, nil
}

func (s *Store) DeleteTemplateHost(ctx context.Context, orgID, hostID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM egress_template_hosts h USING egress_templates t
		 WHERE h.id=$1 AND h.template_id=t.id AND t.org_id=$2`, hostID, orgID)
	return err
}

// --- Assignment + agent-owned hosts ---

// AgentEgress bundles an agent's egress configuration for the UI.
type AgentEgress struct {
	TemplateIDs []uuid.UUID `json:"template_ids"`
	Hosts       []Host      `json:"hosts"`
}

func (s *Store) AgentConfig(ctx context.Context, agentID uuid.UUID) (AgentEgress, error) {
	out := AgentEgress{TemplateIDs: []uuid.UUID{}, Hosts: []Host{}}
	trows, err := s.pool.Query(ctx, `SELECT template_id FROM agent_egress_templates WHERE agent_id=$1`, agentID)
	if err != nil {
		return out, err
	}
	defer trows.Close()
	for trows.Next() {
		var id uuid.UUID
		if err := trows.Scan(&id); err != nil {
			return out, err
		}
		out.TemplateIDs = append(out.TemplateIDs, id)
	}
	if err := trows.Err(); err != nil {
		return out, err
	}
	hrows, err := s.pool.Query(ctx, `SELECT id, pattern, note FROM agent_egress_hosts WHERE agent_id=$1 ORDER BY pattern`, agentID)
	if err != nil {
		return out, err
	}
	defer hrows.Close()
	for hrows.Next() {
		var h Host
		if err := hrows.Scan(&h.ID, &h.Pattern, &h.Note); err != nil {
			return out, err
		}
		out.Hosts = append(out.Hosts, h)
	}
	return out, hrows.Err()
}

// SetAgentTemplate assigns a template or removes the assignment.
func (s *Store) SetAgentTemplate(ctx context.Context, agentID, templateID uuid.UUID, assigned bool) error {
	if assigned {
		_, err := s.pool.Exec(ctx,
			`INSERT INTO agent_egress_templates (agent_id, template_id) VALUES ($1,$2)
			 ON CONFLICT DO NOTHING`, agentID, templateID)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`DELETE FROM agent_egress_templates WHERE agent_id=$1 AND template_id=$2`, agentID, templateID)
	return err
}

func (s *Store) AddAgentHost(ctx context.Context, agentID uuid.UUID, pattern, note string) (Host, error) {
	norm, err := NormalizePattern(pattern)
	if err != nil {
		return Host{}, err
	}
	var h Host
	err = s.pool.QueryRow(ctx,
		`INSERT INTO agent_egress_hosts (agent_id, pattern, note) VALUES ($1,$2,$3)
		 ON CONFLICT (agent_id, pattern) DO UPDATE SET note=EXCLUDED.note
		 RETURNING id, pattern, note`,
		agentID, norm, strings.TrimSpace(note)).Scan(&h.ID, &h.Pattern, &h.Note)
	if err != nil {
		return Host{}, err
	}
	return h, nil
}

func (s *Store) DeleteAgentHost(ctx context.Context, agentID, hostID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM agent_egress_hosts WHERE id=$1 AND agent_id=$2`, hostID, agentID)
	return err
}

// --- Base allowlist (org-wide, for every agent) ---

// ListDefaultHosts returns the organisation's base allowlist — hosts every
// agent may reach. Configurable; seeded with the LLM endpoint.
func (s *Store) ListDefaultHosts(ctx context.Context, orgID uuid.UUID) ([]Host, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, pattern, note FROM egress_default_hosts WHERE org_id=$1 ORDER BY pattern`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Host{}
	for rows.Next() {
		var h Host
		if err := rows.Scan(&h.ID, &h.Pattern, &h.Note); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) AddDefaultHost(ctx context.Context, orgID uuid.UUID, pattern, note string) (Host, error) {
	norm, err := NormalizePattern(pattern)
	if err != nil {
		return Host{}, err
	}
	var h Host
	err = s.pool.QueryRow(ctx,
		`INSERT INTO egress_default_hosts (org_id, pattern, note) VALUES ($1,$2,$3)
		 ON CONFLICT (org_id, pattern) DO UPDATE SET note=EXCLUDED.note
		 RETURNING id, pattern, note`,
		orgID, norm, strings.TrimSpace(note)).Scan(&h.ID, &h.Pattern, &h.Note)
	if err != nil {
		return Host{}, err
	}
	return h, nil
}

func (s *Store) DeleteDefaultHost(ctx context.Context, orgID, hostID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM egress_default_hosts WHERE id=$1 AND org_id=$2`, hostID, orgID)
	return err
}

// EffectiveAllowlist resolves an agent's complete pattern list: the org's base
// allowlist + the hosts of all assigned templates + agent-owned hosts. Only the
// environment additions (COVEY_EGRESS_ALLOW) are added on top in the proxy.
func (s *Store) EffectiveAllowlist(ctx context.Context, agentID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT th.pattern FROM agent_egress_templates at
		  JOIN egress_template_hosts th ON th.template_id=at.template_id
		  WHERE at.agent_id=$1
		UNION
		SELECT pattern FROM agent_egress_hosts WHERE agent_id=$1
		UNION
		SELECT d.pattern FROM egress_default_hosts d
		  JOIN agents a ON a.org_id=d.org_id WHERE a.id=$1`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Per-sandbox token ---

// HashToken hashes an egress token for storage/comparison.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// SetAgentToken stores (rotates) the token hash of an agent.
func (s *Store) SetAgentToken(ctx context.Context, agentID uuid.UUID, tokenHash string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO agent_egress_tokens (agent_id, token_hash, updated_at) VALUES ($1,$2,now())
		 ON CONFLICT (agent_id) DO UPDATE SET token_hash=EXCLUDED.token_hash, updated_at=now()`,
		agentID, tokenHash)
	return err
}

// AgentTokenHash returns the stored token hash (empty if there is none).
func (s *Store) AgentTokenHash(ctx context.Context, agentID uuid.UUID) (string, error) {
	var h string
	err := s.pool.QueryRow(ctx, `SELECT token_hash FROM agent_egress_tokens WHERE agent_id=$1`, agentID).Scan(&h)
	return h, err
}

// --- Decision log ---

func (s *Store) LogDecision(ctx context.Context, agentID uuid.UUID, host, method string, allowed bool) error {
	var aid any
	if agentID != uuid.Nil {
		aid = agentID
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO egress_log (agent_id, host, method, allowed) VALUES ($1,$2,$3,$4)`,
		aid, host, method, allowed)
	return err
}

// ListLog returns the organisation's most recent decisions, optionally only
// blocked ones or only those of one agent, with the agent slug for display.
func (s *Store) ListLog(ctx context.Context, orgID, agentID uuid.UUID, onlyBlocked bool, limit int) ([]LogEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT l.id, l.agent_id, COALESCE(a.slug,''), l.host, l.method, l.allowed, l.created_at
	      FROM egress_log l JOIN agents a ON a.id=l.agent_id WHERE a.org_id=$1`
	args := []any{orgID}
	if agentID != uuid.Nil {
		args = append(args, agentID)
		q += fmt.Sprintf(" AND l.agent_id=$%d", len(args))
	}
	if onlyBlocked {
		q += " AND l.allowed=false"
	}
	args = append(args, limit)
	q += fmt.Sprintf(" ORDER BY l.id DESC LIMIT $%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		var created time.Time
		if err := rows.Scan(&e.ID, &e.AgentID, &e.AgentSlug, &e.Host, &e.Method, &e.Allowed, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = created.Format(time.RFC3339)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Stats summarises the egress activity of the last 24 hours — for the overview
// tiles in the monitoring view.
type Stats struct {
	Allowed24h int         `json:"allowed_24h"`
	Blocked24h int         `json:"blocked_24h"`
	TopBlocked []HostCount `json:"top_blocked"`
}

// HostCount counts blocked accesses per host.
type HostCount struct {
	Host  string `json:"host"`
	Count int    `json:"count"`
}

// LogStats returns the organisation's 24h summary.
func (s *Store) LogStats(ctx context.Context, orgID uuid.UUID) (Stats, error) {
	st := Stats{TopBlocked: []HostCount{}}
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FILTER (WHERE l.allowed), COUNT(*) FILTER (WHERE NOT l.allowed)
		 FROM egress_log l JOIN agents a ON a.id=l.agent_id
		 WHERE a.org_id=$1 AND l.created_at > now() - interval '24 hours'`,
		orgID).Scan(&st.Allowed24h, &st.Blocked24h)
	if err != nil {
		return st, err
	}
	rows, err := s.pool.Query(ctx,
		`SELECT l.host, COUNT(*) FROM egress_log l JOIN agents a ON a.id=l.agent_id
		 WHERE a.org_id=$1 AND NOT l.allowed AND l.created_at > now() - interval '24 hours'
		 GROUP BY l.host ORDER BY COUNT(*) DESC, l.host LIMIT 5`, orgID)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	for rows.Next() {
		var hc HostCount
		if err := rows.Scan(&hc.Host, &hc.Count); err != nil {
			return st, err
		}
		st.TopBlocked = append(st.TopBlocked, hc)
	}
	return st, rows.Err()
}

// CleanupLog deletes log entries older than the given age.
func (s *Store) CleanupLog(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM egress_log WHERE created_at < now() - $1::interval`,
		fmt.Sprintf("%d seconds", int(olderThan.Seconds())))
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// NormalizePattern validates and normalises a host pattern: lower case,
// trimmed, without scheme/path/port, optionally with a "*." prefix. A port that
// was passed along is removed — the allowlist only matches the host, and what
// is stored is exactly what is enforced.
func NormalizePattern(raw string) (string, error) {
	p := strings.ToLower(strings.TrimSpace(raw))
	if p == "" {
		return "", fmt.Errorf("%w: empty", ErrInvalidPattern)
	}
	if strings.ContainsAny(p, " \t/") || strings.Contains(p, "://") {
		return "", fmt.Errorf("%w: host only (no scheme/path/whitespace)", ErrInvalidPattern)
	}
	wildcard := strings.HasPrefix(p, "*.")
	host := strings.TrimPrefix(p, "*.")
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || !strings.Contains(host, ".") {
		return "", fmt.Errorf("%w: not a valid host", ErrInvalidPattern)
	}
	if wildcard {
		return "*." + host, nil
	}
	return host, nil
}
