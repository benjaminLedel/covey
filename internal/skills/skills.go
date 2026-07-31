// Package skills verwaltet die Fähigkeiten eines Agenten als eigenes Objekt.
//
// Der Unterschied zur übrigen Agenten-Config (agents.CompilePrompt) ist die
// Ladezeit: SOUL.md, CAPABILITIES.md und PLAYBOOKS.md stehen in JEDEM Lauf
// vollständig im System-Prompt. Für Identität und Grenzen ist das richtig —
// sie gelten immer. Für Prozeduren ist es Verschwendung: Ein Agent mit fünf
// Playbooks zahlt alle fünf, auch wenn der Lauf nach drei Turns feststellt,
// dass nichts zu tun ist.
//
// Ein Skill kehrt das um. Er ist ein Verzeichnis mit einer SKILL.md, deren
// YAML-Frontmatter (name + description) immer sichtbar ist, während Rumpf und
// Zusatzdateien erst geladen werden, wenn Claude den Skill zieht. Der Daemon
// startet den Lauf mit HOME=<Agenten-Home>; ein Verzeichnis unter
// <home>/.claude/skills/<name>/ ist damit ein PERSÖNLICHER Skill genau dieses
// Agenten (Claude-Code-Doku: „Personal — ~/.claude/skills/<skill-name>/SKILL.md").
//
// Zwei Ebenen, dem Secrets-Modell nachgebaut: Skills der Org-Bibliothek
// (AgentID leer) werden an Agenten verlinkt, agent-eigene Skills gehören einem.
// Ohne Verlinkung erreicht ein Bibliotheks-Skill niemanden — dieselbe
// Opt-in-Regel wie bei secret_assignments.
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
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("skill nicht gefunden")
	// ErrInvalidName: der Name wird zum Verzeichnisnamen und zum
	// /slash-command — er muss beides gefahrlos hergeben.
	ErrInvalidName = errors.New("ungültiger skill-name")
	// ErrInvalidPath: ein Dateipfad, der aus dem Skill-Verzeichnis
	// herausführen würde. Fail-closed, bevor irgendetwas auf Platte landet.
	ErrInvalidPath = errors.New("ungültiger dateipfad")
)

// EntryFile ist der Pflichtname der Skill-Beschreibung. Fehlt sie, ist das
// Verzeichnis für Claude Code kein Skill.
const EntryFile = "SKILL.md"

const (
	// maxFiles/maxFileBytes begrenzen einen Skill. Nicht als Schikane: Der
	// Inhalt wird bei jedem Lauf in die Sandbox materialisiert, und ein
	// versehentlich hochgeladenes Archiv würde jeden Start verlangsamen.
	maxFiles     = 32
	maxFileBytes = 256 << 10
	// maxDescription: die Beschreibung steht dauerhaft im Kontext JEDES Laufs
	// dieses Agenten. Wer hier einen Absatz schreibt, zahlt ihn immer.
	maxDescription = 500
)

// nameRe hält den Namen auf dem, was Verzeichnis und Slash-Command vertragen.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Skill ist ein Skill ohne seine Dateien — die Listensicht für UI und API.
// AgentID leer = Skill der Org-Bibliothek. AssignedTo trägt bei
// Bibliotheks-Skills die verlinkten Agenten; leer heißt: wirkt bei niemandem.
type Skill struct {
	ID          uuid.UUID   `json:"id"`
	OrgID       uuid.UUID   `json:"org_id"`
	AgentID     *uuid.UUID  `json:"agent_id,omitempty"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	AssignedTo  []uuid.UUID `json:"assigned_to,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at"`
}

// Library sagt, ob der Skill aus der Org-Bibliothek stammt (statt einem
// Agenten zu gehören). Das ist der Herkunftsvermerk, den auch das Bundle trägt.
func (s Skill) Library() bool { return s.AgentID == nil }

// File ist eine Datei im Skill-Verzeichnis. Path ist relativ, validiert.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Full ist ein Skill mit Dateien — was zum Materialisieren und zum Export nötig ist.
type Full struct {
	Skill
	Files []File `json:"files"`
}

// Store ist der Datenzugriff. Keine Schnittstelle mit Alternativimplementierung
// (anders als SecretStore/IdentityProvider): Skills sind Covey-eigener Zustand,
// kein Fremdsystem, das man austauschen wollen würde.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ValidateName prüft den Skill-Namen. Bewusst streng: Aus ihm werden ein
// Verzeichnis im Agenten-Home und ein Slash-Command.
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("%w %q: erlaubt sind Kleinbuchstaben, Ziffern und Bindestriche (max. 63, Beginn alphanumerisch)",
			ErrInvalidName, name)
	}
	return nil
}

// ValidatePath prüft einen Dateipfad innerhalb des Skill-Verzeichnisses.
//
// Das ist eine Sicherheitsgrenze, keine Kosmetik: Der Pfad wird beim
// Materialisieren an filepath.Join gehängt. Ein "../../.claude/settings.json"
// oder ein absoluter Pfad würde sonst aus dem Skill-Verzeichnis ausbrechen und
// in fremde Teile des Agenten-Home schreiben. Deshalb fail-closed und mit
// path.Clean gegengeprüft, statt nur auf ".." zu suchen.
func ValidatePath(p string) error {
	if p == "" || strings.ContainsRune(p, 0) {
		return fmt.Errorf("%w: leer", ErrInvalidPath)
	}
	if strings.HasPrefix(p, "/") || strings.Contains(p, `\`) || strings.Contains(p, ":") {
		return fmt.Errorf("%w %q: nur relative Pfade mit /", ErrInvalidPath, p)
	}
	cleaned := path.Clean(p)
	if cleaned != p || cleaned == "." || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return fmt.Errorf("%w %q: muss ein normalisierter Pfad innerhalb des Skills sein", ErrInvalidPath, p)
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w %q: leeres oder relatives Pfadsegment", ErrInvalidPath, p)
		}
	}
	return nil
}

// validateFiles prüft den kompletten Dateisatz eines Skills: Pfade, Größen,
// Anzahl — und dass die SKILL.md dabei ist. Ohne sie ist das Verzeichnis für
// Claude Code kein Skill, der Agent bekäme also stumm nichts.
func validateFiles(files []File) error {
	if len(files) == 0 {
		return fmt.Errorf("keine dateien: %s ist pflicht", EntryFile)
	}
	if len(files) > maxFiles {
		return fmt.Errorf("zu viele dateien (%d, max. %d)", len(files), maxFiles)
	}
	seen := map[string]bool{}
	hasEntry := false
	for _, f := range files {
		if err := ValidatePath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("datei %q doppelt", f.Path)
		}
		seen[f.Path] = true
		if len(f.Content) > maxFileBytes {
			return fmt.Errorf("datei %q zu groß (%d bytes, max. %d)", f.Path, len(f.Content), maxFileBytes)
		}
		if f.Path == EntryFile {
			hasEntry = true
		}
	}
	if !hasEntry {
		return fmt.Errorf("%s fehlt — ohne sie erkennt die Runtime das Verzeichnis nicht als Skill", EntryFile)
	}
	return nil
}

// Spec beschreibt einen anzulegenden oder zu ändernden Skill. AgentID leer =
// Org-Bibliothek.
type Spec struct {
	Name        string
	Description string
	AgentID     *uuid.UUID
	Files       []File
}

// Upsert legt einen Skill an oder ersetzt ihn vollständig (Beschreibung und
// Dateisatz). Ersetzen statt Zusammenführen ist Absicht: Der Aufrufer schickt
// den gewünschten Endzustand, sonst bliebe eine gelöschte Datei ewig liegen.
func (s *Store) Upsert(ctx context.Context, orgID uuid.UUID, spec Spec) (Skill, error) {
	if err := ValidateName(spec.Name); err != nil {
		return Skill{}, err
	}
	desc := strings.TrimSpace(spec.Description)
	if desc == "" {
		return Skill{}, errors.New("description fehlt — sie entscheidet, ob die Runtime den Skill überhaupt lädt")
	}
	if len([]rune(desc)) > maxDescription {
		return Skill{}, fmt.Errorf("description zu lang (%d zeichen, max. %d) — sie steht in jedem Lauf im Kontext",
			len([]rune(desc)), maxDescription)
	}
	if err := validateFiles(spec.Files); err != nil {
		return Skill{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Skill{}, err
	}
	defer tx.Rollback(ctx)

	// Die Teil-Unique-Indizes trennen Org- und Agent-Ebene, deshalb zwei Wege
	// statt eines ON CONFLICT.
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
			return Skill{}, err
		}
	case err != nil:
		return Skill{}, err
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

// Get liefert einen Skill samt Dateien.
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

// ListLibrary liefert die Org-Bibliothek (ohne Dateien), je Eintrag mit den
// verlinkten Agenten — die UI zeigt daran, was wirklich irgendwo wirkt.
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

// ListOwn liefert die Skills, die einem Agenten selbst gehören (ohne Dateien).
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

// Delete entfernt einen Skill samt Dateien und Zuweisungen (ON DELETE CASCADE).
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

// Assign verlinkt einen Bibliotheks-Skill mit einem Agenten. Agent-eigene
// Skills lassen sich nicht verlinken — sie gehören bereits jemandem.
func (s *Store) Assign(ctx context.Context, orgID, skillID, agentID uuid.UUID) error {
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
		return errors.New("nur bibliotheks-skills lassen sich zuweisen — dieser gehört bereits einem agenten")
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO skill_assignments (skill_id, agent_id)
		VALUES ($1,$2) ON CONFLICT DO NOTHING`, skillID, agentID)
	return err
}

// Unassign löst die Verlinkung.
func (s *Store) Unassign(ctx context.Context, orgID, skillID, agentID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM skill_assignments a USING skills s
		WHERE a.skill_id=s.id AND s.org_id=$1 AND a.skill_id=$2 AND a.agent_id=$3`,
		orgID, skillID, agentID)
	return err
}

// ForAgent löst auf, was ein Agent tatsächlich bekommt: seine eigenen Skills
// plus die ihm verlinkten Bibliotheks-Skills, mit Dateien.
//
// Bei Namensgleichheit gewinnt der agent-eigene Skill. Sonst könnte eine
// Änderung an der Bibliothek eine bewusste lokale Abweichung stillschweigend
// überschreiben — und auf Platte könnten zwei Skills nicht ins selbe
// Verzeichnis.
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
		// ORDER BY agent_id NULLS LAST: Der agent-eigene Eintrag kommt zuerst
		// und bleibt stehen.
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
