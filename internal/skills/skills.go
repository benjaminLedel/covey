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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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
	// maxFiles/maxFileBytes/maxSkillBytes cap a skill. Not out of spite: the
	// content is materialized into the sandbox on every run and travels in one
	// daemon message (16 MiB read limit, base64 for binary files), and an
	// archive uploaded by accident would slow down every start. Measured
	// against published skills: most hold a dozen files under 200 KiB, the
	// largest 96 files, and one with bundled fonts 5.4 MiB.
	maxFiles      = 256
	maxFileBytes  = 1 << 20
	maxSkillBytes = 8 << 20
	// maxDescription is the specification's limit. The description permanently
	// sits in the context of EVERY run of this agent — the editor says so; the
	// limit itself is the format's, so that a valid skill is not refused.
	maxDescription = 1024
	// maxCompatibility is the specification's limit as well.
	maxCompatibility = 500
	// maxLicense, maxAllowedTools and the metadata caps are covey's: the
	// specification sets none, and every one of them ends up in a single
	// frontmatter line of every run.
	maxLicense       = 500
	maxAllowedTools  = 1024
	maxMetadata      = 32
	maxMetadataKey   = 64
	maxMetadataValue = 1024
	// maxName is the specification's limit.
	maxName = 64
)

// nameRe is the specification's name rule: lowercase letters, digits and
// single hyphens, neither leading nor trailing. The name becomes the directory
// name and the /slash-command, so it has to be safe for both.
var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// MaxBodyBytes bounds a request carrying one skill: its files at the limit,
// base64 and JSON on top.
const MaxBodyBytes = maxSkillBytes*3/2 + 1<<20

// EncodingBase64 marks a binary file: Content holds its bytes base64-encoded.
const EncodingBase64 = "base64"

// Skill is a skill without its files — the list view for UI and API.
// AgentID empty = skill from the org library. For library skills AssignedTo
// carries the linked agents; empty means: it takes effect nowhere.
type Skill struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	AgentID     *uuid.UUID `json:"agent_id,omitempty"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Meta
	AssignedTo []uuid.UUID `json:"assigned_to,omitempty"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// Library tells whether the skill comes from the org library (rather than
// belonging to an agent). It is the origin marker the bundle carries as well.
func (s Skill) Library() bool { return s.AgentID == nil }

// File is a file inside the skill directory. Path is relative and validated.
//
// Encoding empty = a text file, Content is its UTF-8 text. EncodingBase64 = a
// binary file (an image in assets/, a spreadsheet template), Content is its
// bytes in base64 — JSON carries no raw bytes, and a text column none either.
type File struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Encoding string `json:"encoding,omitempty"`
}

// Binary tells whether the file carries bytes rather than text.
func (f File) Binary() bool { return f.Encoding == EncodingBase64 }

// Bytes returns the file's raw content.
func (f File) Bytes() ([]byte, error) {
	switch f.Encoding {
	case "":
		return []byte(f.Content), nil
	case EncodingBase64:
		b, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			return nil, fmt.Errorf("%w: file %q is not valid base64", ErrInvalid, f.Path)
		}
		return b, nil
	}
	return nil, fmt.Errorf("%w: file %q has unknown encoding %q (empty for text, %q for binary)",
		ErrInvalid, f.Path, f.Encoding, EncodingBase64)
}

// FileOf builds a File from raw bytes: text when it is valid UTF-8 without NUL
// bytes, base64 otherwise. That is the decision an archive import needs — it
// does not know which files are text.
func FileOf(p string, data []byte) File {
	if utf8.Valid(data) && !strings.ContainsRune(string(data), 0) {
		return File{Path: p, Content: string(data)}
	}
	return File{Path: p, Content: base64.StdEncoding.EncodeToString(data), Encoding: EncodingBase64}
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

// ValidateName checks the skill name against the specification. It turns into a
// directory in the agent home and into a slash command.
func ValidateName(name string) error {
	if len(name) > maxName || !nameRe.MatchString(name) {
		return fmt.Errorf("%w %q: 1-%d characters, lowercase letters, digits and single hyphens, not at the start or end",
			ErrInvalidName, name, maxName)
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
	total := 0
	for _, f := range files {
		if err := ValidatePath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("%w: file %q duplicated", ErrInvalid, f.Path)
		}
		seen[f.Path] = true
		data, err := f.Bytes()
		if err != nil {
			return err
		}
		if len(data) > maxFileBytes {
			return fmt.Errorf("%w: file %q too large (%d bytes, max. %d)", ErrInvalid, f.Path, len(data), maxFileBytes)
		}
		total += len(data)
		// Postgres refuses NUL bytes and invalid UTF-8 in a text column — as a
		// 500 that looks like a server fault. Said here, it is an instruction.
		if !f.Binary() && (!utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0)) {
			return fmt.Errorf("%w: file %q is not UTF-8 text — send it with encoding %q",
				ErrInvalid, f.Path, EncodingBase64)
		}
		if f.Path == EntryFile {
			if f.Binary() {
				return fmt.Errorf("%w: %s must be text", ErrInvalid, EntryFile)
			}
			hasEntry = true
		}
	}
	if total > maxSkillBytes {
		return fmt.Errorf("%w: files too large in all (%d bytes, max. %d)", ErrInvalid, total, maxSkillBytes)
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
	Meta
	AgentID *uuid.UUID
	Files   []File
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
	if err := maxRunes("description", desc, maxDescription); err != nil {
		return err
	}
	if err := validateMeta(spec.Meta); err != nil {
		return err
	}
	return validateFiles(spec.Files)
}

// Check runs Validate and returns every problem it finds rather than the first —
// what an import preview and `covey skills validate` show. Each check is
// independent, so a skill with a bad name still has its files looked at.
func Check(spec Spec) []string {
	var out []string
	add := func(err error) {
		if err != nil {
			out = append(out, strings.TrimPrefix(err.Error(), ErrInvalid.Error()+": "))
		}
	}
	add(ValidateName(spec.Name))
	desc := strings.TrimSpace(spec.Description)
	if desc == "" {
		add(fmt.Errorf("%w: description missing — it decides whether the runtime loads the skill at all", ErrInvalid))
	} else {
		add(maxRunes("description", desc, maxDescription))
	}
	add(validateMeta(spec.Meta))
	add(validateFiles(spec.Files))
	return out
}

func maxRunes(field, v string, limit int) error {
	if n := utf8.RuneCountInString(v); n > limit {
		return fmt.Errorf("%w: %s too long (%d characters, max. %d)", ErrInvalid, field, n, limit)
	}
	return nil
}

func validateMeta(m Meta) error {
	if err := maxRunes("license", strings.TrimSpace(m.License), maxLicense); err != nil {
		return err
	}
	if err := maxRunes("compatibility", strings.TrimSpace(m.Compatibility), maxCompatibility); err != nil {
		return err
	}
	if err := maxRunes("allowed-tools", strings.TrimSpace(m.AllowedTools), maxAllowedTools); err != nil {
		return err
	}
	if len(m.Metadata) > maxMetadata {
		return fmt.Errorf("%w: too many metadata entries (%d, max. %d)", ErrInvalid, len(m.Metadata), maxMetadata)
	}
	for k, v := range m.Metadata {
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k, "\r\n") {
			return fmt.Errorf("%w: metadata key %q must be a non-empty single line", ErrInvalid, k)
		}
		if err := maxRunes("metadata key "+k, k, maxMetadataKey); err != nil {
			return err
		}
		if err := maxRunes("metadata value of "+k, v, maxMetadataValue); err != nil {
			return err
		}
	}
	return nil
}

// Assemble turns a set of files plus form values into a Spec — the one place
// where a SKILL.md frontmatter meets the values given beside it. Every write
// path goes through here: the API, the bundle import, the archive import.
//
// If the SKILL.md carries a frontmatter — because someone pasted a finished
// file, imported a bundle or an archive — it is cut off and fills in what the
// form leaves empty. covey keeps the fields as columns; were they in the file as
// well, the two could contradict each other and nobody would know which applies.
// A frontmatter key outside the specification is an error (see ParseEntry).
func Assemble(form Front, agentID *uuid.UUID, files []File) (Spec, error) {
	spec := Spec{Name: form.Name, Description: form.Description, Meta: form.Meta,
		AgentID: agentID, Files: make([]File, 0, len(files))}
	for _, f := range files {
		if f.Path == EntryFile && !f.Binary() {
			front, body, err := ParseEntry(f.Content)
			if err != nil {
				return Spec{}, err
			}
			if spec.Name == "" {
				spec.Name = front.Name
			}
			if spec.Description == "" {
				spec.Description = front.Description
			}
			if spec.License == "" {
				spec.License = front.License
			}
			if spec.Compatibility == "" {
				spec.Compatibility = front.Compatibility
			}
			if spec.AllowedTools == "" {
				spec.AllowedTools = front.AllowedTools
			}
			if spec.Metadata == nil {
				spec.Metadata = front.Metadata
			}
			f.Content = body
		}
		spec.Files = append(spec.Files, f)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.License = strings.TrimSpace(spec.License)
	spec.Compatibility = strings.TrimSpace(spec.Compatibility)
	spec.AllowedTools = strings.TrimSpace(spec.AllowedTools)
	if len(spec.Metadata) == 0 {
		spec.Metadata = nil
	}
	return spec, nil
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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(ctx)
	sk, err := writeTx(ctx, tx, orgID, spec, createOnly)
	if err != nil {
		return Skill{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Skill{}, err
	}
	return sk, nil
}

// WriteAll stores several skills in ONE transaction — the archive import. Half
// an imported repository would be worse than none: either every skill lands or
// none does. replace decides what happens with a name already taken at its
// level (see Create and Upsert).
func (s *Store) WriteAll(ctx context.Context, orgID uuid.UUID, specs []Spec, replace bool) ([]Skill, error) {
	for _, spec := range specs {
		if err := Validate(spec); err != nil {
			return nil, fmt.Errorf("skill %s: %w", spec.Name, err)
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	out := make([]Skill, 0, len(specs))
	for _, spec := range specs {
		sk, err := writeTx(ctx, tx, orgID, spec, !replace)
		if err != nil {
			return nil, err
		}
		out = append(out, sk)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func writeTx(ctx context.Context, tx pgx.Tx, orgID uuid.UUID, spec Spec, createOnly bool) (Skill, error) {
	desc := strings.TrimSpace(spec.Description)
	meta := spec.Meta
	if meta.Metadata == nil {
		meta.Metadata = map[string]string{}
	}
	metaJSON, err := json.Marshal(meta.Metadata)
	if err != nil {
		return Skill{}, err
	}

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
		if _, err := tx.Exec(ctx, `INSERT INTO skills (id, org_id, agent_id, name, description,
			license, compatibility, allowed_tools, metadata)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, orgID, spec.AgentID, spec.Name, desc,
			meta.License, meta.Compatibility, meta.AllowedTools, metaJSON); err != nil {
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
		if _, err := tx.Exec(ctx, `UPDATE skills SET description=$2, license=$3, compatibility=$4,
			allowed_tools=$5, metadata=$6, updated_at=now() WHERE id=$1`,
			id, desc, meta.License, meta.Compatibility, meta.AllowedTools, metaJSON); err != nil {
			return Skill{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM skill_files WHERE skill_id=$1`, id); err != nil {
			return Skill{}, err
		}
	}
	for _, f := range spec.Files {
		// A binary file keeps its bytes in data and leaves content empty;
		// data IS NULL marks a text file (migration 0133).
		content, data := f.Content, []byte(nil)
		if f.Binary() {
			if data, err = f.Bytes(); err != nil {
				return Skill{}, err
			}
			content = ""
		}
		if _, err := tx.Exec(ctx, `INSERT INTO skill_files (skill_id, path, content, data) VALUES ($1,$2,$3,$4)`,
			id, f.Path, content, data); err != nil {
			return Skill{}, err
		}
	}
	return Skill{ID: id, OrgID: orgID, AgentID: spec.AgentID, Name: spec.Name,
		Description: desc, Meta: spec.Meta, UpdatedAt: time.Now()}, nil
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
	err := scanSkill(s.pool.QueryRow(ctx, `SELECT `+skillCols+`
		FROM skills WHERE org_id=$1 AND id=$2`, orgID, id), &out.Skill)
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
		`SELECT path, content, data FROM skill_files WHERE skill_id=$1 ORDER BY path`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		var data []byte
		if err := rows.Scan(&f.Path, &f.Content, &data); err != nil {
			return nil, err
		}
		if data != nil {
			f.Content, f.Encoding = base64.StdEncoding.EncodeToString(data), EncodingBase64
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
	rows, err := s.pool.Query(ctx, `SELECT `+skillCols+`
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
	rows, err := s.pool.Query(ctx, `SELECT `+skillCols+`
		FROM skills WHERE org_id=$1 AND agent_id=$2 ORDER BY name`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	return scanSkills(rows)
}

// skillCols is the column list scanSkill reads, in its order.
const skillCols = `id, org_id, agent_id, name, description, license, compatibility, allowed_tools, metadata, updated_at`

func scanSkill(row pgx.Row, sk *Skill) error {
	var metaJSON []byte
	if err := row.Scan(&sk.ID, &sk.OrgID, &sk.AgentID, &sk.Name, &sk.Description,
		&sk.License, &sk.Compatibility, &sk.AllowedTools, &metaJSON, &sk.UpdatedAt); err != nil {
		return err
	}
	sk.Metadata = nil
	if len(metaJSON) > 0 {
		if err := json.Unmarshal(metaJSON, &sk.Metadata); err != nil {
			return err
		}
	}
	if len(sk.Metadata) == 0 {
		sk.Metadata = nil
	}
	return nil
}

func scanSkills(rows pgx.Rows) ([]Skill, error) {
	defer rows.Close()
	out := []Skill{}
	for rows.Next() {
		var sk Skill
		if err := scanSkill(rows, &sk); err != nil {
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
// Assign links a library skill to an agent. Both SIDES are checked against
// the organisation — the skill and the agent.
//
// Before, only the first half stood here and the second was missing: an own
// skill could be attached to a FOREIGN agent with it. A skill
// is an instruction that goes into its prompt — that would be text that one
// organisation writes into the agent of another (FR-003, finding D).
// The pattern comes from secrets.Assign, which has always done it right.
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
	rows, err := s.pool.Query(ctx, `SELECT `+skillCols+`
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
