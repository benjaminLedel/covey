// Package agents holds the agent registry and the config compilation
// (SOUL.md & co. → system prompt), see spec/02-agent-model.md.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/org"
	"covey/internal/sandbox"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Agent states of the state machine from spec/03-lifecycle-scheduling.md.
// 'blocked' lives on the task, not on the agent: an agent with a parked task
// is 'sleeping' again and can work on something else.
const (
	StatusSleeping  = "sleeping"
	StatusTriggered = "triggered"
	StatusTriage    = "triage"
	StatusWorking   = "working"
	// StatusSecuring: the run is over, the platform is not. The sandbox is
	// being stopped and the home written into the store — seconds for a small
	// home, half a minute for a grown one. It gets a name of its own because
	// `working` answered "is this agent busy" with a yes that meant something
	// else entirely, and hid the one moment where an operator would want to
	// know what is going on.
	StatusSecuring = "securing"
	StatusKilled   = "killed"
)

type Agent struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Runtime     string    `json:"runtime"`
	// Model picks the LLM within the runtime (e.g. claude-opus-4-8);
	// empty = the runtime uses its own default.
	Model string `json:"model"`
	// Effort sets the reasoning effort within the runtime (Claude Code's
	// --effort: low, medium, high, xhigh, max); empty = the runtime's own
	// default. Independent of Model — a model can run at any effort level.
	Effort string `json:"effort"`
	// MaxTurns caps the turns of a runtime run (runaway guard);
	// 0 = the orchestrator's default.
	MaxTurns int        `json:"max_turns"`
	Status   string     `json:"status"`
	OwnerID  *uuid.UUID `json:"owner_id,omitempty"`
	// SupervisorID is the place in the org chart: the human the agent
	// reports and escalates to (spec/02).
	SupervisorID *uuid.UUID `json:"supervisor_id,omitempty"`
	// DepartmentID assigns the agent to a department; nil = none.
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	// Profile is the same employee master data as for humans
	// (org.Profile): function, contact, platform identifiers and the values of
	// the org-wide configurable profile fields — agents are employees (spec/02).
	org.Profile
	Killed    bool    `json:"killed"`
	BudgetUSD float64 `json:"budget_usd"`
	// RuntimeID is the contract this agent works on (spec/18). nil = not yet
	// assigned; Runtime then still names the engine, for the migration window
	// and for agents created before the assignment existed.
	RuntimeID *uuid.UUID `json:"runtime_id,omitempty"`
	// RecordingLevel is the optional agent override of the recording depth
	// (spec/06); empty = inherits the org floor. It only ever tightens (max with
	// the floor), enforced in the control plane.
	RecordingLevel string `json:"recording_level"`
	// RecordingRetentionDays is the optional agent override of how long the
	// verbatim run is kept (spec/06); nil = inherits the organisation. It only
	// ever EXTENDS: an agent may keep longer than the organisation requires,
	// never shorter. 0 = forever.
	RecordingRetentionDays *int `json:"recording_retention_days,omitempty"`
	// SandboxImage is the workplace this agent runs in: either a profile name
	// (`base`, `dev`) or an image reference of its own. Empty = the instance
	// default. The image hangs off the agent and not off the instance (D11) —
	// a mail agent should not carry a developer agent's JVM, and on a runner
	// the image is at the same time a statement of capacity: it reports which
	// ones it holds and gets only matching agents (spec/16).
	SandboxImage string `json:"sandbox_image"`
	// RunnerTags are the capabilities this agent needs of its host: arm64, gpu,
	// a runner inside the target system's network. Empty = any runner of the
	// organisation (spec/16, "Scheduling").
	RunnerTags []string `json:"runner_tags,omitempty"`
	// Services run beside this agent's sandbox for as long as it lives: the
	// database a test suite needs, the queue an application talks to. They are
	// the half of a workplace the image cannot carry — a project's database is
	// not part of Covey, and building it into the image made every agent pay
	// for it (spec/16, "Services beside the sandbox").
	//
	// It sits on the agent because that is what the platform has when a sandbox
	// starts. An agent working in several projects will outgrow that, and the
	// runner protocol is deliberately indifferent to where the list came from.
	Services []sandbox.Service `json:"services,omitempty"`
	// WarmSandbox keeps this agent's sandbox alive between waking phases
	// (opt-in): dev servers and caches survive, the next run starts without a
	// cold build-up. Default false → ephemeral like everyone else (spec/01).
	WarmSandbox bool `json:"warm_sandbox"`
	// HiredAt is the agent's first day. nil = a draft: it exists and can be
	// configured, but it is not dispatched, has no heartbeat, no live webhook,
	// no sandbox and no cost (spec/20). Hiring is a human act — no action of the
	// platform's own target system can perform it.
	HiredAt *time.Time `json:"hired_at,omitempty"`
	// WebhookToken is the secret of the optional generic webhook trigger
	// (nil = disabled). Deliberately not in the JSON — readable only through
	// the dedicated webhook endpoint (manager roles).
	WebhookToken *string   `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Draft: created, not yet hired. Read at every point that would otherwise
// start a waking phase.
func (a Agent) Draft() bool { return a.HiredAt == nil }

type ConfigVersion struct {
	ID             uuid.UUID         `json:"id"`
	AgentID        uuid.UUID         `json:"agent_id"`
	Version        int               `json:"version"`
	Files          map[string]string `json:"files"`
	CompiledPrompt string            `json:"compiled_prompt"`
	CreatedAt      time.Time         `json:"created_at"`
}

type SystemAccess struct {
	System string   `json:"system"`
	Scopes []string `json:"scopes"`
	// Tools is the agent's tool allowlist for this system (MCP);
	// empty = all tools allowed. It is materialised not here but in the
	// target store (agent_target_tools) — ACCESS.md is the textual view.
	Tools []string `json:"tools,omitempty"`
}

var ErrNotFound = errors.New("agent not found")

// ErrAmbiguousSlug: this slug exists in more than one organisation, so an
// address that carries only the slug does not name an agent.
var ErrAmbiguousSlug = errors.New("this slug exists in several organisations")

type Registry struct {
	pool *pgxpool.Pool
	// SystemHeartbeats are platform-wide default heartbeats (source='system' in
	// agent_heartbeats) that the control plane materialises for every agent —
	// unless it defines a HEARTBEAT.md entry of the same name itself.
	// Currently: the configurable wiki cleanup heartbeat (COVEY_WIKI_CLEANUP).
	// Set during wiring in cmd/covey; empty = no defaults.
	SystemHeartbeats []Heartbeat
}

func NewRegistry(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }

const agentCols = "id, org_id, slug, display_name, runtime, model, effort, max_turns, status, owner_id, supervisor_id, department_id, job_title, identities, phone, responsibilities, custom, killed, budget_usd, runtime_id, webhook_token, COALESCE(recording_level,''), recording_retention_days, sandbox_image, runner_tags, services, warm_sandbox, hired_at, created_at, updated_at"

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.OrgID, &a.Slug, &a.DisplayName, &a.Runtime, &a.Model, &a.Effort, &a.MaxTurns, &a.Status,
		&a.OwnerID, &a.SupervisorID, &a.DepartmentID, &a.JobTitle, &a.Identities, &a.Phone, &a.Responsibilities, &a.Custom,
		&a.Killed, &a.BudgetUSD, &a.RuntimeID, &a.WebhookToken, &a.RecordingLevel, &a.RecordingRetentionDays, &a.SandboxImage, &a.RunnerTags, &a.Services, &a.WarmSandbox, &a.HiredAt, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (r *Registry) Create(ctx context.Context, orgID uuid.UUID, slug, displayName, runtime string, ownerID *uuid.UUID) (Agent, error) {
	return r.create(ctx, orgID, slug, displayName, runtime, ownerID, false)
}

// CreateDraft creates an agent that has not been hired yet: it exists, can be
// configured and looked at, and is skipped by the dispatch loop until a human
// hires it (spec/20). The way in for everything that produces an agent without
// somebody having finished thinking about it — a template, an import, and above
// all the People department, whose output is other agents.
func (r *Registry) CreateDraft(ctx context.Context, orgID uuid.UUID, slug, displayName, runtime string, ownerID *uuid.UUID) (Agent, error) {
	return r.create(ctx, orgID, slug, displayName, runtime, ownerID, true)
}

// DefaultRuntime is the engine an agent gets when none is named — the one every
// bundle without a `runtime` field ends up on. Named here so that whoever has to
// validate against the engine BEFORE the agent exists (the bundle import) asks
// the same question as create() answers.
const DefaultRuntime = "claude-code"

// Covey Doctor trägt einen festen Namen.
//
// Er ist kein Kollege, den eine Organisation sich ausdenkt, sondern die
// Plattform, die sich selbst betrachtet (spec/21) — dieselbe Rolle, die
// `covey doctor` vor einem Upgrade einnimmt. Ein Agent, der jeden Kollegen
// beurteilen und für ihn Änderungen vorschlagen darf, soll überall gleich
// heißen: wer in einem fremden Recording „Covey Doctor" liest, weiß, was das
// war, ohne die ACCESS.md nachzuschlagen. Deshalb Name und Slug reserviert und
// gegen Umbenennen gesperrt — ein umbenannter Doctor wäre ein Agent mit den
// Rechten des Doctors und dem Namen eines Kollegen.
const (
	DoctorSlug = "covey-doctor"
	DoctorName = "Covey Doctor"
)

// IsDoctor erkennt Covey Doctor am reservierten Slug. Der Slug ist
// der Anker und nicht der Anzeigename, weil er eindeutig je Organisation ist —
// und er ist mitgesperrt, sonst wäre das Umbenennen des Slugs der Umweg um die
// Namenssperre.
func IsDoctor(a Agent) bool { return a.Slug == DoctorSlug }

func (r *Registry) create(ctx context.Context, orgID uuid.UUID, slug, displayName, runtime string, ownerID *uuid.UUID, draft bool) (Agent, error) {
	if runtime == "" {
		runtime = DefaultRuntime
	}
	// Der reservierte Slug bringt den Namen mit — hier und nicht im Handler,
	// damit jeder Weg ihn erbt: Oberfläche, Bundle-Import, Entwurf. Sonst hinge
	// die feste Identität daran, welchen Weg jemand genommen hat, und die
	// Sperre gegen Umbenennen fände beim Import schon einen falschen Namen vor.
	if slug == DoctorSlug {
		displayName = DoctorName
	}
	hired := "now()"
	if draft {
		hired = "NULL"
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO agents (id, org_id, slug, display_name, runtime, owner_id, hired_at)
		VALUES ($1,$2,$3,$4,$5,$6,`+hired+`) RETURNING `+agentCols,
		uuid.New(), orgID, slug, displayName, runtime, ownerID)
	return scanAgent(row)
}

// Hire ends the draft state: from this moment the agent is dispatched, its
// heartbeat is scheduled and its queued tasks are released. Idempotent — the
// hiring date is not overwritten, because it is a fact about a day that has
// already happened.
func (r *Registry) Hire(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		"UPDATE agents SET hired_at=COALESCE(hired_at, now()), updated_at=now() WHERE id=$1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (r *Registry) Get(ctx context.Context, id uuid.UUID) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE id=$1", id))
}

// ConfigVersionMeta is the slim history of an agent config: version and
// timestamp, without the files themselves. For diagnostic views that want to
// show the trail without loading every revision.
type ConfigVersionMeta struct {
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}

// ConfigHistory returns the versions of an agent config in ascending order.
func (r *Registry) ConfigHistory(ctx context.Context, agentID uuid.UUID) ([]ConfigVersionMeta, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT version, created_at FROM agent_config_versions WHERE agent_id=$1 ORDER BY version", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConfigVersionMeta
	for rows.Next() {
		var m ConfigVersionMeta
		if err := rows.Scan(&m.Version, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// FindBySlug looks an agent up by slug alone, without knowing the
// organisation — for paths where the organisation is not yet established (the
// webhook endpoint carries only the slug in the URL). Slugs are globally unique
// enough for this purpose; on ambiguity the oldest one wins.
// FindBySlug resolves a slug ACROSS organisations — the webhook address knows
// no tenant, because the target system that calls it has no account.
//
// It therefore refuses as soon as the slug is not unique instance-wide.
// Before, it took the oldest agent (`ORDER BY created_at LIMIT 1`), and that
// was a misdelivery waiting for a second tenant: slugs are unique only PER
// organisation (`UNIQUE (org_id, slug)`), and the names a second company picks
// are the ones the first picked — support, qa, dev. The ticket of organisation
// B would have been worked on in organisation A's sandbox and answered with
// its credentials (FR-003, finding B).
//
// The way out of the ambiguity is the address, not the resolution: the agent
// id and the trigger token are unique instance-wide.
func (r *Registry) FindBySlug(ctx context.Context, slug string) (Agent, error) {
	rows, err := r.pool.Query(ctx,
		"SELECT "+agentCols+" FROM agents WHERE slug=$1 ORDER BY created_at LIMIT 2", slug)
	if err != nil {
		return Agent{}, err
	}
	defer rows.Close()
	var gefunden []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return Agent{}, err
		}
		gefunden = append(gefunden, a)
	}
	if err := rows.Err(); err != nil {
		return Agent{}, err
	}
	switch len(gefunden) {
	case 0:
		return Agent{}, ErrNotFound
	case 1:
		return gefunden[0], nil
	default:
		return Agent{}, ErrAmbiguousSlug
	}
}

func (r *Registry) GetBySlug(ctx context.Context, orgID uuid.UUID, slug string) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE org_id=$1 AND slug=$2", orgID, slug))
}

func (r *Registry) List(ctx context.Context, orgID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+agentCols+" FROM agents WHERE org_id=$1 ORDER BY created_at", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Registry) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET status=$2, updated_at=now() WHERE id=$1", id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetKilled flips the kill switch of a single agent (spec/06).
func (r *Registry) SetKilled(ctx context.Context, id uuid.UUID, killed bool) error {
	status := StatusKilled
	if !killed {
		status = StatusSleeping
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET killed=$2, status=$3, updated_at=now() WHERE id=$1", id, killed, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetFleetKilled is the fleet-wide emergency stop.
func (r *Registry) SetFleetKilled(ctx context.Context, orgID uuid.UUID, killed bool) error {
	_, err := r.pool.Exec(ctx, "UPDATE organizations SET fleet_killed=$2 WHERE id=$1", orgID, killed)
	return err
}

func (r *Registry) FleetKilled(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var v bool
	err := r.pool.QueryRow(ctx, "SELECT fleet_killed FROM organizations WHERE id=$1", orgID).Scan(&v)
	return v, err
}

func (r *Registry) SetBudget(ctx context.Context, id uuid.UUID, budgetUSD float64) error {
	_, err := r.pool.Exec(ctx, "UPDATE agents SET budget_usd=$2, updated_at=now() WHERE id=$1", id, budgetUSD)
	return err
}

// SetRuntime switches an agent's runtime. Takes effect at the next task
// dispatch — running sessions stay untouched.
func (r *Registry) SetRuntime(ctx context.Context, id uuid.UUID, runtime string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET runtime=$2, updated_at=now() WHERE id=$1", id, runtime)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetModel sets the agent's model (empty = runtime default).
// Takes effect at the next task dispatch, like SetRuntime.
func (r *Registry) SetModel(ctx context.Context, id uuid.UUID, model string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET model=$2, updated_at=now() WHERE id=$1", id, model)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetEffort sets the agent's reasoning effort (Claude Code's --effort: low,
// medium, high, xhigh, max; empty = runtime default). Independent of the
// model — takes effect at the next task dispatch, like SetModel.
func (r *Registry) SetEffort(ctx context.Context, id uuid.UUID, effort string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET effort=$2, updated_at=now() WHERE id=$1", id, effort)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetMaxTurns sets the turn limit per runtime run (0 = the orchestrator's
// default). Takes effect at the next task dispatch, like SetModel.
func (r *Registry) SetMaxTurns(ctx context.Context, id uuid.UUID, maxTurns int) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET max_turns=$2, updated_at=now() WHERE id=$1", id, maxTurns)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetRecordingLevel sets the agent override of the recording depth (spec/06);
// empty = back to inheriting the org floor (NULL). The effective level is always
// max(org floor, override) — enforced on the control plane side.
func (r *Registry) SetRecordingLevel(ctx context.Context, id uuid.UUID, level string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET recording_level=NULLIF($2,''), updated_at=now() WHERE id=$1", id, level)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetRecordingRetention sets the agent override of how long the verbatim run is
// kept (spec/06); nil = back to inheriting the organisation (NULL).
//
// The effective value is the organisation's, EXTENDED by this one — never
// shortened. That is enforced where the deletion happens (observability), not
// here, because it is a statement about the pair and not about this value: an
// agent set to 30 days under an organisation that keeps 365 still keeps 365.
// Storing the smaller number is therefore allowed and simply has no effect,
// which is the honest behaviour — silently rewriting somebody's input to 365
// would hide the rule instead of applying it.
func (r *Registry) SetRecordingRetention(ctx context.Context, id uuid.UUID, tage *int) error {
	if tage != nil && *tage < 0 {
		null := 0
		tage = &null
	}
	tag, err := r.pool.Exec(ctx,
		"UPDATE agents SET recording_retention_days=$2, updated_at=now() WHERE id=$1", id, tage)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SandboxImagesInUse counts the agents per configured workplace — the basis for
// the startup check "is the image these agents need actually there?". Drafts
// count too: they are hired at some point, and then the image has to exist.
func (r *Registry) SandboxImagesInUse(ctx context.Context) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT sandbox_image, count(*) FROM agents WHERE NOT killed GROUP BY sandbox_image`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var image string
		var n int
		if err := rows.Scan(&image, &n); err != nil {
			return nil, err
		}
		out[image] = n
	}
	return out, rows.Err()
}

// AgentsPerWorkplace names the agents per workplace instead of counting them —
// the difference between "3 agents" and "which three".
//
// The count answered a question nobody has. Whoever looks at a workplace wants
// to know whom it concerns before they change or delete it, and the name is the
// only form of that answer that survives being read out loud.
func (r *Registry) AgentsPerWorkplace(ctx context.Context, orgID uuid.UUID) (map[string][]AgentRef, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT sandbox_image, id, slug, display_name FROM agents
		  WHERE NOT killed AND org_id=$1 ORDER BY display_name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]AgentRef{}
	for rows.Next() {
		var image string
		var a AgentRef
		if err := rows.Scan(&image, &a.ID, &a.Slug, &a.DisplayName); err != nil {
			return nil, err
		}
		out[image] = append(out[image], a)
	}
	return out, rows.Err()
}

// AgentRef is an agent in a list that is about something else — enough to name
// it and to link to it.
type AgentRef struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
}

// SandboxImagesInUseForOrg is the same count for one organisation — what the
// workplace list in the interface shows. The instance-wide figure belongs to
// the doctor, which asks for the host; whoever is looking at their own
// organisation must not be told how many agents the neighbours run.
func (r *Registry) SandboxImagesInUseForOrg(ctx context.Context, orgID uuid.UUID) (map[string]int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT sandbox_image, count(*) FROM agents WHERE NOT killed AND org_id=$1 GROUP BY sandbox_image`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var image string
		var n int
		if err := rows.Scan(&image, &n); err != nil {
			return nil, err
		}
		out[image] = n
	}
	return out, rows.Err()
}

// SetSandboxImage sets the agent's workplace: a profile name (`base`, `dev`),
// an image reference of its own, or empty for the instance default. Takes
// effect at the next cold start — a running or warm-parked sandbox keeps the
// image it was started with.
func (r *Registry) SetSandboxImage(ctx context.Context, id uuid.UUID, image string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET sandbox_image=$2, updated_at=now() WHERE id=$1",
		id, strings.TrimSpace(image))
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetRunnerTags sets the capabilities an agent needs of its host. Empty = any
// runner of the organisation. Takes effect at the next cold start.
func (r *Registry) SetRunnerTags(ctx context.Context, id uuid.UUID, tags []string) error {
	clean := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag != "" {
			clean = append(clean, tag)
		}
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET runner_tags=$2, updated_at=now() WHERE id=$1", id, clean)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetServices sets what runs beside this agent's sandbox. Empty = nothing,
// which is where every agent starts. Takes effect at the next cold start: the
// services are brought up with the sandbox, so a running or warm-parked one
// keeps the set it was started with.
//
// The declaration is validated here rather than trusted from the caller — this
// is the last place before it becomes a container name and a host name on a
// shared runner.
func (r *Registry) SetServices(ctx context.Context, id uuid.UUID, services []sandbox.Service) error {
	clean, err := sandbox.ValidateServices(services)
	if err != nil {
		return err
	}
	if clean == nil {
		clean = []sandbox.Service{}
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET services=$2, updated_at=now() WHERE id=$1", id, clean)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetWarmSandbox turns the warm sandbox on/off for an agent. Takes effect from
// the next falling asleep on: with true the sandbox is no longer torn down.
func (r *Registry) SetWarmSandbox(ctx context.Context, id uuid.UUID, warm bool) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET warm_sandbox=$2, updated_at=now() WHERE id=$1", id, warm)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Rename changes the display name. The slug is changed separately via SetSlug.
// Delete removes an agent for good. All dependent data (config, backlog,
// secrets, assignments) is deleted along with it via ON DELETE CASCADE.
// orgID prevents cross-org deletion via guessed IDs.
func (r *Registry) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "DELETE FROM agents WHERE id=$1 AND org_id=$2", id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// supervisor_id no longer carries a DB foreign key (migration 0025):
	// detach referencing subordinates here so no reference dangles.
	_, _ = r.pool.Exec(ctx, "UPDATE agents SET supervisor_id=NULL WHERE supervisor_id=$1 AND org_id=$2", id, orgID)
	return nil
}

func (r *Registry) Rename(ctx context.Context, id uuid.UUID, displayName string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET display_name=$2, updated_at=now() WHERE id=$1", id, displayName)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetSlug changes an agent's slug. The new slug has to be unique within the
// org — the DB unique constraint (org_id, slug) sees to that.
// Permitted format: lowercase letters, digits and hyphens only.
func (r *Registry) SetSlug(ctx context.Context, id uuid.UUID, slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("slug may only contain lowercase letters, digits and hyphens")
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET slug=$2, updated_at=now() WHERE id=$1", id, slug)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return fmt.Errorf("slug already taken")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWebhookToken sets or rotates the token of the generic webhook trigger;
// nil disables the webhook (spec/03, wake source event).
func (r *Registry) SetWebhookToken(ctx context.Context, id uuid.UUID, token *string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET webhook_token=$2, updated_at=now() WHERE id=$1", id, token)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// GetByWebhookToken resolves the trigger token to the agent —
// the authentication of the public webhook endpoint.
func (r *Registry) GetByWebhookToken(ctx context.Context, token string) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE webhook_token=$1", token))
}

// SupervisorName returns the display name of the manager from the org chart —
// for escalation texts. Empty if no manager is assigned.
func (r *Registry) SupervisorName(ctx context.Context, agentID uuid.UUID) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(h.display_name, sa.display_name, '')
		FROM agents a
		LEFT JOIN humans h ON h.id = a.supervisor_id
		LEFT JOIN agents sa ON sa.id = a.supervisor_id
		WHERE a.id=$1`, agentID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// SetSupervisor re-hangs the agent in the org chart; nil clears the assignment.
func (r *Registry) SetSupervisor(ctx context.Context, id uuid.UUID, supervisorID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET supervisor_id=$2, updated_at=now() WHERE id=$1", id, supervisorID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// ProfileUpdate is a partial profile update — nil fields stay unchanged; the
// maps replace the entire stock when non-nil (the store normalises).
// Mirror image of the profile fields of org.HumanUpdate.
type ProfileUpdate struct {
	JobTitle         *string
	Identities       map[string]string
	Phone            *string
	Responsibilities *string
	Custom           map[string]string
}

// UpdateProfile writes the agent's employee master data. orgID prevents
// cross-org access via guessed IDs.
func (r *Registry) UpdateProfile(ctx context.Context, orgID, id uuid.UUID, upd ProfileUpdate) (Agent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback(ctx)

	a, err := scanAgent(tx.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE id=$1 AND org_id=$2 FOR UPDATE", id, orgID))
	if err != nil {
		return Agent{}, err
	}
	if upd.JobTitle != nil {
		a.JobTitle = *upd.JobTitle
	}
	if upd.Identities != nil {
		a.Identities = org.NormalizeIdentities(upd.Identities)
	}
	if upd.Phone != nil {
		a.Phone = *upd.Phone
	}
	if upd.Responsibilities != nil {
		a.Responsibilities = *upd.Responsibilities
	}
	if upd.Custom != nil {
		a.Custom = org.NormalizeCustom(upd.Custom)
	}
	if a.Identities == nil {
		a.Identities = map[string]string{}
	}
	if a.Custom == nil {
		a.Custom = map[string]string{}
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET job_title=$2, identities=$3, phone=$4,
			responsibilities=$5, custom=$6, updated_at=now() WHERE id=$1`,
		id, a.JobTitle, a.Identities, a.Phone, a.Responsibilities, a.Custom); err != nil {
		return Agent{}, err
	}
	return a, tx.Commit(ctx)
}

// SetDepartment assigns the agent to a department; nil clears the assignment.
func (r *Registry) SetDepartment(ctx context.Context, id uuid.UUID, deptID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET department_id=$2, updated_at=now() WHERE id=$1", id, deptID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SaveConfig creates a new config version (existing ones are never edited),
// compiles the system prompt and materialises ACCESS.md for the broker.
func (r *Registry) SaveConfig(ctx context.Context, agentID uuid.UUID, files map[string]string, createdBy *uuid.UUID) (ConfigVersion, error) {
	compiled := CompilePrompt(files)
	accesses := ParseAccess(files["ACCESS.md"])
	heartbeats, err := ParseHeartbeat(files["HEARTBEAT.md"])
	if err != nil {
		return ConfigVersion{}, fmt.Errorf("HEARTBEAT.md: %w", err)
	}
	// The indicators are not stored here — they are read from the config when
	// the evaluation runs. Parsing them is a validation: a rule with a typo
	// would count nothing, and nothing looks exactly like a lazy agent.
	if _, err := ParseKPIs(files["KPIS.md"]); err != nil {
		return ConfigVersion{}, fmt.Errorf("KPIS.md: %w", err)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ConfigVersion{}, err
	}
	defer tx.Rollback(ctx)

	var version int
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(MAX(version),0)+1 FROM agent_config_versions WHERE agent_id=$1", agentID).Scan(&version); err != nil {
		return ConfigVersion{}, err
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return ConfigVersion{}, err
	}
	cv := ConfigVersion{ID: uuid.New(), AgentID: agentID, Version: version, Files: files, CompiledPrompt: compiled}
	if err := tx.QueryRow(ctx, `INSERT INTO agent_config_versions (id, agent_id, version, files, compiled_prompt, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		cv.ID, agentID, version, filesJSON, compiled, createdBy).Scan(&cv.CreatedAt); err != nil {
		return ConfigVersion{}, err
	}

	if _, err := tx.Exec(ctx, "DELETE FROM system_accesses WHERE agent_id=$1", agentID); err != nil {
		return ConfigVersion{}, err
	}
	for _, acc := range accesses {
		// Ohne Scope ist die leere Liste, nicht NULL. `- system: zammad` ohne
		// `scope:` ist eine Zeile, die ein Mensch schreibt — sie bedeutet „kein
		// Scope vergeben" und muss sich speichern lassen. Vorher ging sie als
		// NULL in eine NOT-NULL-Spalte, und der Mensch bekam beim Speichern der
		// Config einen SQLSTATE-Fehler statt eines Agenten ohne Zugriff.
		scopes := acc.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		if _, err := tx.Exec(ctx, "INSERT INTO system_accesses (agent_id, system, scopes) VALUES ($1,$2,$3)",
			agentID, acc.System, scopes); err != nil {
			return ConfigVersion{}, err
		}
	}

	// Materialise the heartbeats: upsert per titel, so that last_fired_at
	// survives across config versions; delete removed entries.
	names := make([]string, 0, len(heartbeats))
	for _, hb := range heartbeats {
		names = append(names, hb.Name)
		var everySeconds *int64
		var dailyAt *string
		if hb.Every > 0 {
			s := int64(hb.Every / time.Second)
			everySeconds = &s
		} else {
			dailyAt = &hb.DailyAt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_heartbeats (agent_id, name, task_body, every_seconds, daily_at, only_if, source)
			VALUES ($1,$2,$3,$4,$5,$6,'config')
			ON CONFLICT (agent_id, name) DO UPDATE
			SET task_body=EXCLUDED.task_body, every_seconds=EXCLUDED.every_seconds, daily_at=EXCLUDED.daily_at,
			    only_if=EXCLUDED.only_if, source='config'`,
			agentID, hb.Name, hb.Task, everySeconds, dailyAt, hb.OnlyIf); err != nil {
			return ConfigVersion{}, err
		}
	}
	// Only clean up the entries originating from HEARTBEAT.md (source='config') —
	// platform-wide system defaults (source='system') stay untouched.
	if _, err := tx.Exec(ctx, "DELETE FROM agent_heartbeats WHERE agent_id=$1 AND source='config' AND NOT (name = ANY($2))",
		agentID, names); err != nil {
		return ConfigVersion{}, err
	}
	// Materialise the system default heartbeats for this agent (source='system'),
	// unless it has a HEARTBEAT.md entry of the same name itself (override).
	// That way freshly created agents get the defaults at the first config sync too.
	for _, hb := range r.SystemHeartbeats {
		if slices.Contains(names, hb.Name) {
			continue
		}
		var everySeconds *int64
		var dailyAt *string
		if hb.Every > 0 {
			s := int64(hb.Every / time.Second)
			everySeconds = &s
		} else {
			dailyAt = &hb.DailyAt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_heartbeats (agent_id, name, task_body, every_seconds, daily_at, only_if, source)
			VALUES ($1,$2,$3,$4,$5,$6,'system')
			ON CONFLICT (agent_id, name) DO UPDATE
			SET task_body=EXCLUDED.task_body, every_seconds=EXCLUDED.every_seconds, daily_at=EXCLUDED.daily_at,
			    only_if=EXCLUDED.only_if
			WHERE agent_heartbeats.source='system'`,
			agentID, hb.Name, hb.Task, everySeconds, dailyAt, hb.OnlyIf); err != nil {
			return ConfigVersion{}, err
		}
	}
	return cv, tx.Commit(ctx)
}

// ReconcileSystemHeartbeats reconciles the platform-wide system default heartbeats
// (r.SystemHeartbeats) across ALL agents — called at process start, so that existing
// agents receive the defaults even without another config sync and removed defaults
// disappear. It touches source='system' rows only; an agent's own HEARTBEAT.md
// heartbeats (source='config') stay untouched, and an agent with an entry of the
// same name of its own is skipped (the override wins).
func (r *Registry) ReconcileSystemHeartbeats(ctx context.Context) error {
	names := make([]string, 0, len(r.SystemHeartbeats))
	for _, hb := range r.SystemHeartbeats {
		names = append(names, hb.Name)
		var everySeconds *int64
		var dailyAt *string
		if hb.Every > 0 {
			s := int64(hb.Every / time.Second)
			everySeconds = &s
		} else {
			dailyAt = &hb.DailyAt
		}
		if _, err := r.pool.Exec(ctx, `INSERT INTO agent_heartbeats (agent_id, name, task_body, every_seconds, daily_at, only_if, source)
			SELECT a.id, $1, $2, $3, $4, '', 'system' FROM agents a
			WHERE NOT EXISTS (SELECT 1 FROM agent_heartbeats h
				WHERE h.agent_id=a.id AND h.name=$1 AND h.source='config')
			ON CONFLICT (agent_id, name) DO UPDATE
			SET task_body=EXCLUDED.task_body, every_seconds=EXCLUDED.every_seconds, daily_at=EXCLUDED.daily_at
			WHERE agent_heartbeats.source='system'`,
			hb.Name, hb.Task, everySeconds, dailyAt); err != nil {
			return err
		}
	}
	// Remove orphaned system defaults (schedule changed -> name stays the same;
	// feature switched off -> names empty -> all source='system' rows go).
	if _, err := r.pool.Exec(ctx,
		"DELETE FROM agent_heartbeats WHERE source='system' AND NOT (name = ANY($1))", names); err != nil {
		return err
	}
	return nil
}

// CurrentConfig returns the agent's most recent config version.
func (r *Registry) CurrentConfig(ctx context.Context, agentID uuid.UUID) (ConfigVersion, error) {
	var cv ConfigVersion
	var filesJSON []byte
	err := r.pool.QueryRow(ctx, `SELECT id, agent_id, version, files, compiled_prompt, created_at
		FROM agent_config_versions WHERE agent_id=$1 ORDER BY version DESC LIMIT 1`, agentID).
		Scan(&cv.ID, &cv.AgentID, &cv.Version, &filesJSON, &cv.CompiledPrompt, &cv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return cv, ErrNotFound
	}
	if err != nil {
		return cv, err
	}
	if err := json.Unmarshal(filesJSON, &cv.Files); err != nil {
		return cv, fmt.Errorf("config files: %w", err)
	}
	return cv, nil
}

// ConfigAtVersion returns one specific config version — the basis a proposal
// was written against (spec/21). Everything else reads the latest one; this is
// the only place that needs an older one, and it needs it to answer whether
// somebody edited the same file underneath an open proposal.
func (r *Registry) ConfigAtVersion(ctx context.Context, agentID uuid.UUID, version int) (ConfigVersion, error) {
	var cv ConfigVersion
	var filesJSON []byte
	err := r.pool.QueryRow(ctx, `SELECT id, agent_id, version, files, compiled_prompt, created_at
		FROM agent_config_versions WHERE agent_id=$1 AND version=$2`, agentID, version).
		Scan(&cv.ID, &cv.AgentID, &cv.Version, &filesJSON, &cv.CompiledPrompt, &cv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return cv, ErrNotFound
	}
	if err != nil {
		return cv, err
	}
	if err := json.Unmarshal(filesJSON, &cv.Files); err != nil {
		return cv, fmt.Errorf("config files: %w", err)
	}
	return cv, nil
}

// Accesses returns the materialised accesses from ACCESS.md (broker check).
func (r *Registry) Accesses(ctx context.Context, agentID uuid.UUID) ([]SystemAccess, error) {
	rows, err := r.pool.Query(ctx, "SELECT system, scopes FROM system_accesses WHERE agent_id=$1", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SystemAccess
	for rows.Next() {
		var a SystemAccess
		if err := rows.Scan(&a.System, &a.Scopes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// HasAccess checks agent→system(+scope) — the broker's authorisation question.
func (r *Registry) HasAccess(ctx context.Context, agentID uuid.UUID, system, scope string) (bool, error) {
	accs, err := r.Accesses(ctx, agentID)
	if err != nil {
		return false, err
	}
	for _, a := range accs {
		if a.System != system {
			continue
		}
		if scope == "" {
			return true, nil
		}
		for _, s := range a.Scopes {
			if s == scope {
				return true, nil
			}
		}
	}
	return false, nil
}
