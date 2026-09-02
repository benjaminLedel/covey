// Package skills manages an agent's capabilities as objects of their own.
//
// What sets them apart from the rest of the agent config (agents.CompilePrompt)
// is load time: SOUL.md, CAPABILITIES.md and PLAYBOOKS.md sit in the system
// prompt in full on EVERY run. For identity and boundaries that is right — they
// always apply. For procedures it is waste: an agent with five playbooks pays
// for all five, even when the run finds after three turns that there is nothing
// to do.
//
// A skill inverts that. It is a directory holding a SKILL.md whose YAML
// frontmatter (name + description) is always visible, while body and extra
// files are only loaded once Claude pulls the skill. The daemon starts the run
// with HOME=<agent home>; a directory under <home>/.claude/skills/<name>/ is
// therefore a PERSONAL skill of exactly this agent (Claude Code docs:
// "Personal — ~/.claude/skills/<skill-name>/SKILL.md").
//
// Two levels, modelled on the secrets scheme: skills in the org library
// (AgentID empty) are linked to agents, agent-owned skills belong to one.
// Without a link, a library skill reaches nobody — the same opt-in rule as with
// secret_assignments.
package skills

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("skill not found")
	// ErrInvalid marks an input error made by the caller — missing description,
	// oversized file, missing SKILL.md. Every check below wraps it so the HTTP
	// layer turns it into a 400 and not a 500 that looks like a server fault.
	ErrInvalid = errors.New("invalid skill")
	// ErrInvalidName: the name becomes the directory name and the
	// /slash-command — it has to be safe for both.
	ErrInvalidName = fmt.Errorf("%w: invalid skill name", ErrInvalid)
	// ErrInvalidPath: a file path that would lead out of the skill directory.
	// Fail-closed, before anything hits the disk.
	ErrInvalidPath = fmt.Errorf("%w: invalid file path", ErrInvalid)
	// ErrExists: at this level (library or agent) a skill of that name already
	// exists. Creating does NOT silently replace it — the name is the directory
	// name, so a slip would overwrite someone else's work.
	ErrExists = errors.New("a skill with this name already exists")
)

// EntryFile is the mandatory name of the skill description. Without it the
// directory is not a skill as far as Claude Code is concerned.
const EntryFile = "SKILL.md"

const (
	// maxFiles/maxFileBytes cap a skill. Not out of spite: the content is
	// materialized into the sandbox on every run, and an archive uploaded by
	// accident would slow down every start.
	maxFiles     = 32
	maxFileBytes = 256 << 10
	// maxDescription: the description permanently sits in the context of EVERY
	// run of this agent. Write a paragraph here and you pay for it every time.
	maxDescription = 500
)

// nameRe keeps the name within what a directory and a slash command tolerate.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Skill is a skill without its files — the list view for UI and API.
// AgentID empty = skill from the org library. For library skills AssignedTo
// carries the linked agents; empty means: it takes effect nowhere.
type Skill struct {
	ID          uuid.UUID   `json:"id"`
	OrgID       uuid.UUID   `json:"org_id"`
	AgentID     *uuid.UUID  `json:"agent_id,omitempty"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	AssignedTo  []uuid.UUID `json:"assigned_to,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Library tells whether the skill comes from the org library (rather than
// belonging to an agent). It is the origin marker the bundle carries as well.
func (s Skill) Library() bool { return s.AgentID == nil }

// File is a file inside the skill directory. Path is relative and validated.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Full is a skill with its files — what materializing and exporting need.
type Full struct {
	Skill
	Files []File `json:"files"`
}

// Store is the data access layer. Not an interface with an alternative
// implementation (unlike SecretStore/IdentityProvider): skills are covey's own
// state, not a foreign system anyone would want to swap out.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ValidateName checks the skill name. Deliberately strict: it turns into a
// directory in the agent home and into a slash command.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w %q: allowed are lowercase letters, digits and hyphens (max. 63, must start alphanumeric)",
			ErrInvalidName, name)
	}
	return nil
}

// ValidatePath checks a file path inside the skill directory.
//
// This is a security boundary, not cosmetics: while materializing, the path is
// appended via filepath.Join. A "../../.claude/settings.json" or an absolute
// path would otherwise break out of the skill directory and write into other
// parts of the agent home. Hence fail-closed and cross-checked with path.Clean
// instead of merely searching for "..".
func ValidatePath(p string) error {
	if p == "" || strings.ContainsRune(p, 0) {
		return fmt.Errorf("%w: empty", ErrInvalidPath)
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.Contains(p, ":") {
		return fmt.Errorf("%w %q: only relative paths with /", ErrInvalidPath, p)
	}
	cleaned := path.Clean(p)
	if cleaned != p || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return fmt.Errorf("%w %q: must be a normalized path inside the skill", ErrInvalidPath, p)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w %q: empty or relative path segment", ErrInvalidPath, p)
		}
	}
	return nil
}

// validateFiles checks a skill's complete file set: paths, sizes, count — and
// that SKILL.md is among them. Without it the directory is not a skill to
// Claude Code, so the agent would silently get nothing.
func validateFiles(files []File) error {
	if len(files) == 0 {
		return fmt.Errorf("%w: no files, %s is mandatory", ErrInvalid, EntryFile)
	}
	if len(files) > maxFiles {
		return fmt.Errorf("%w: too many files (%d, max. %d)", ErrInvalid, len(files), maxFiles)
	}
	seen := map[string]bool{}
	hasEntry := false
	for _, f := range files {
		if err := ValidatePath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("%w: file %q duplicated", ErrInvalid, f.Path)
		}
		seen[f.Path] = true
		if len(f.Content) > maxFileBytes {
			return fmt.Errorf("%w: file %q too large (%d bytes, max. %d)", ErrInvalid, f.Path, len(f.Content), maxFileBytes)
		}
		if f.Path == EntryFile {
			hasEntry = true
		}
	}
	if !hasEntry {
		return fmt.Errorf("%w: %s missing — without it the runtime does not recognize the directory as a skill",
			ErrInvalid, EntryFile)
	}
	return nil
}

// Spec describes a skill to be created or changed. AgentID empty = org
// library.
type Spec struct {
	Name        string
	Description string
	AgentID     *uuid.UUID
	Files       []File
}

// Validate checks a Spec without storing anything.
//
// Separate from Upsert because some callers need to know up front whether
// everything will pass: the bundle import validates the whole bundle first and
// only then creates — a half-imported agent would be worse than a rejected one.
func Validate(spec Spec) error {
	if err := ValidateName(spec.Name); err != nil {
		return err
	}
	desc := strings.TrimSpace(spec.Description)
	if desc == "" {
		return fmt.Errorf("%w: description missing — it decides whether the runtime loads the skill at all",
			ErrInvalid)
	}
	if len([]rune(desc)) > maxDescription {
		return fmt.Errorf("%w: description too long (%d characters, max. %d) — it sits in the context of every run",
			ErrInvalid, len([]rune(desc)), maxDescription)
	}
	return validateFiles(spec.Files)
}

// Upsert creates a skill or replaces it entirely (description and file set).
// Replacing instead of merging is deliberate: the caller sends the desired end
// state, otherwise a deleted file would linger forever.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, spec Spec) (Skill, error) {
	return s.write(ctx, orgID, spec, false)
}

// write is the shared write path of Upsert and Create. createOnly decides what
// happens when the name is already taken: replace or ErrExists.
//
// Both in ONE transaction rather than "ask first, then write": a concurrent
// call fits between a separate existence check and the write, and the second
// caller would then silently replace someone else's work although it meant to
// create. The SELECT therefore runs in the same transaction as the INSERT, and
// the partial unique index catches the race the SELECT cannot see.
func (s *Store) write(ctx context.Context, orgID uuid.UUID, spec Spec, createOnly bool) (Skill, error) {
	if err := Validate(spec); err != nil {
		return Skill{}, err
	}
	desc := strings.TrimSpace(spec.Description)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(ctx)

	// The partial unique indexes separate org and agent level, hence two paths
	// instead of a single ON CONFLICT.
	var id uuid.UUID
	var q string
	var args []any
	if spec.AgentID == nil {
		q = `SELECT id FROM skills WHERE org_id=$1 AND name=$2 AND agent_id IS NULL`
		args = []any{orgID, spec.Name}
	} else {
		q = `SELECT id FROM skills WHERE org_id=$1 AND name=$2 AND agent_id=$3`
		args = []any{orgID, spec.Name, *spec.AgentID}
	}
	err = tx.QueryRow(ctx, q, args...).Scan(&id)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		id = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO skills (id, org_id, agent_id, name, description)
			VALUES ($1,$2,$3,$4,$5)`, id, orgID, spec.AgentID, spec.Name, desc); err != nil {
			// Between SELECT and INSERT a concurrent call may have taken the
			// same name. The partial unique index catches that; here it turns
			// into the same statement as with Create, instead of a raw 23505.
			if isUniqueViolation(err) {
				return Skill{}, fmt.Errorf("%w: %q", ErrExists, spec.Name)
			}
			return Skill{}, err
		}
	case err != nil:
		return Skill{}, err
	case createOnly:
		return Skill{}, fmt.Errorf("%w: %q", ErrExists, spec.Name)
	default:
		if _, err := tx.Exec(ctx, `UPDATE skills SET description=$2, updated_at=now() WHERE id=$1`,
			id, desc); err != nil {
			return Skill{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM skill_files WHERE skill_id=$1`, id); err != nil {
			return Skill{}, err
		}
	}
	for _, f := range spec.Files {
		if _, err := tx.Exec(ctx, `INSERT INTO skill_files (skill_id, path, content) VALUES ($1,$2,$3)`,
			id, f.Path, f.Content); err != nil {
			return Skill{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Skill{}, err
	}
	return Skill{ID: id, OrgID: orgID, AgentID: spec.AgentID, Name: spec.Name,
		Description: desc, UpdatedAt: time.Now()}, nil
}

// Create creates a skill and rejects an already taken name with ErrExists
// instead of replacing it.
//
// What sets it apart from Upsert is the caller's intent: whoever hits "create
// new skill" means a new one — a name collision would then be someone else's
// work quietly disappearing. For replacing there is Upsert (editor, import).
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, spec Spec) (Skill, error) {
	return s.write(ctx, orgID, spec, true)
}

// Get returns a skill including its files.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Full, error) {
	var out Full
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, agent_id, name, description, updated_at
		FROM skills WHERE org_id=$1 AND id=$2`, orgID, id).
		Scan(&out.ID, &out.OrgID, &out.AgentID, &out.Name, &out.Description, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Full{}, ErrNotFound
	}
	if err != nil {
		return Full{}, err
	}
	files, err := s.filesOf(ctx, id)
	if err != nil {
		return Full{}, err
	}
	out.Files = files
	if out.Library() {
		if out.AssignedTo, err = s.assigneesOf(ctx, id); err != nil {
			return Full{}, err
		}
	}
	return out, nil
}

func (s *Store) filesOf(ctx context.Context, skillID uuid.UUID) ([]File, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT path, content FROM skill_files WHERE skill_id=$1 ORDER BY path`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.Path, &f.Content); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) assigneesOf(ctx context.Context, skillID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT agent_id FROM skill_assignments WHERE skill_id=$1 ORDER BY created_at`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListLibrary returns the org library (without files), each entry with its
// linked agents — the UI uses that to show what actually takes effect anywhere.
func (s *Store) ListLibrary(ctx context.Context, orgID uuid.UUID) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, name, description, updated_at
		FROM skills WHERE org_id=$1 AND agent_id IS NULL ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	out, err := scanSkills(rows)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].AssignedTo, err = s.assigneesOf(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListOwn returns the skills an agent owns itself (without files).
func (s *Store) ListOwn(ctx context.Context, orgID, agentID uuid.UUID) ([]Skill, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, name, description, updated_at
		FROM skills WHERE org_id=$1 AND agent_id=$2 ORDER BY name`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	return scanSkills(rows)
}

func scanSkills(rows pgx.Rows) ([]Skill, error) {
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		var sk Skill
		if err := rows.Scan(&sk.ID, &sk.OrgID, &sk.AgentID, &sk.Name, &sk.Description, &sk.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	return out, rows.Err()
}

// Delete removes a skill along with its files and assignments (ON DELETE CASCADE).
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM skills WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Assign links a library skill to an agent. Agent-owned skills cannot be
// linked — they already belong to someone.
// Assign verknuepft eine Bibliotheks-Faehigkeit mit einem Agenten. Geprueft
// werden BEIDE Seiten gegen die Organisation — die Faehigkeit und der Agent.
//
// Vorher stand hier nur die erste Haelfte, und die zweite fehlte: eine eigene
// Faehigkeit liess sich damit einem FREMDEN Agenten anhaengen. Eine Faehigkeit
// ist eine Handlungsanweisung, die in seinen Prompt geht — das waere Text, den
// eine Organisation in den Agenten einer anderen schreibt (FR-003, Befund D).
// Das Muster stammt von secrets.Assign, das es seit jeher richtig macht.
func (s *Store) Assign(ctx context.Context, orgID, skillID, agentID uuid.UUID) error {
	var agentGehoert bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM agents WHERE id=$1 AND org_id=$2)`,
		agentID, orgID).Scan(&agentGehoert); err != nil {
		return err
	}
	if !agentGehoert {
		return ErrNotFound
	}
	var isLibrary bool
	err := s.pool.QueryRow(ctx, `SELECT agent_id IS NULL FROM skills WHERE org_id=$1 AND id=$2`,
		orgID, skillID).Scan(&isLibrary)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !isLibrary {
		return fmt.Errorf("%w: only library skills can be assigned — this one already belongs to an agent",
			ErrInvalid)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO skill_assignments (skill_id, agent_id)
		VALUES ($1,$2) ON CONFLICT DO NOTHING`, skillID, agentID)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// Unassign removes the link.
//
// If nothing gets unlinked — unknown ID, foreign organization, no link at all
// — that is ErrNotFound and not success: otherwise the UI confirms a revocation
// that never happened, and nobody spots the typo.
func (s *Store) Unassign(ctx context.Context, orgID, skillID, agentID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM skill_assignments a USING skills s
		WHERE a.skill_id=s.id AND s.org_id=$1 AND a.skill_id=$2 AND a.agent_id=$3`,
		orgID, skillID, agentID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ForAgent resolves what an agent actually gets: its own skills plus the
// library skills linked to it, with files.
//
// On a name clash the agent-owned skill wins. Otherwise a change in the library
// could silently overwrite a deliberate local deviation — and on disk two
// skills could not share the same directory anyway.
func (s *Store) ForAgent(ctx context.Context, orgID, agentID uuid.UUID) ([]Full, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, agent_id, name, description, updated_at
		FROM skills s WHERE s.org_id=$1 AND (
			s.agent_id=$2
			OR (s.agent_id IS NULL AND EXISTS (SELECT 1 FROM skill_assignments a
				WHERE a.skill_id=s.id AND a.agent_id=$2)))
		ORDER BY s.name, s.agent_id NULLS LAST`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	found, err := scanSkills(rows)
	if err != nil {
		return nil, err
	}

	byName := map[string]Skill{}
	for _, sk := range found {
		// ORDER BY agent_id NULLS LAST: the agent-owned entry comes first and
		// is the one that stays.
		if _, dup := byName[sk.Name]; !dup {
			byName[sk.Name] = sk
		}
	}
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Full, 0, len(names))
	for _, n := range names {
		sk := byName[n]
		files, err := s.filesOf(ctx, sk.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, Full{Skill: sk, Files: files})
	}
	return out, nil
}
