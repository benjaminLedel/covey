package notes

import (
	"context"
	"regexp"

	"github.com/google/uuid"
)

// MediaScheme is how a note's Markdown points at a picture in the media
// store: ![](covey-media://<id>) (spec/14). The id alone, no host — the
// reader resolves it against its own instance.
const MediaScheme = "covey-media://"

var mediaRef = regexp.MustCompile(`covey-media://([0-9a-fA-F-]{36})`)

// MediaRefs returns the medium ids a body references, each once.
func MediaRefs(body string) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, m := range mediaRef.FindAllStringSubmatch(body, -1) {
		if id, err := uuid.Parse(m[1]); err == nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// Referenced reports whether any of the seat's notes still points at the
// medium — in its text or as its cover (#372) — the question before a
// picture is removed.
func (s *Store) Referenced(ctx context.Context, humanID, mediumID uuid.UUID) (bool, error) {
	var yes bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM human_notes
		WHERE human_id=$1 AND (body LIKE '%' || $2 || '%' OR cover = $2))`, humanID, MediaScheme+mediumID.String()).Scan(&yes)
	return yes, err
}
