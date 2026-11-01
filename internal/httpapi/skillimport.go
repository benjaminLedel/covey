package httpapi

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/skills"
)

// Importing skills as directories — a zip upload, or a URL to a zip or a
// repository (internal/skills: FromZip, Fetcher).
//
// One archive may hold many skills. Every one of them is checked first; when
// one does not hold up, nothing is stored and the answer lists all problems.
// dry_run returns that same list without storing anything — the preview the
// interface shows before the import, and the place where someone picks single
// skills out of a repository (names).
//
// Two request shapes:
//
//	POST …/skills/import            Content-Type: application/json
//	{"url": "https://github.com/…", "dry_run": true, "replace": false, "names": ["pdf"]}
//
//	POST …/skills/import?dry_run=1&replace=0&names=pdf,docx
//	Content-Type: application/zip   (the archive as the body)

type skillImportRequest struct {
	URL     string   `json:"url"`
	DryRun  bool     `json:"dry_run"`
	Replace bool     `json:"replace"`
	Names   []string `json:"names"`
}

// skillImportItem is one skill of the answer.
type skillImportItem struct {
	Dir         string   `json:"dir"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	License     string   `json:"license,omitempty"`
	Files       int      `json:"files"`
	BinaryFiles int      `json:"binary_files"`
	Exists      bool     `json:"exists"`
	Problems    []string `json:"problems"`
	Skipped     []string `json:"skipped,omitempty"`
	// Status: "ready" (dry run, would be stored), "created", "replaced",
	// "invalid" (has problems, nothing was stored).
	Status string `json:"status"`
	ID     string `json:"id,omitempty"`
}

type skillImportResult struct {
	DryRun   bool              `json:"dry_run"`
	Imported int               `json:"imported"`
	Skills   []skillImportItem `json:"skills"`
}

// handleImportLibrarySkills — POST /api/v1/skills/import.
func (s *Server) handleImportLibrarySkills(w http.ResponseWriter, r *http.Request) {
	s.importSkillArchive(w, r, principalFrom(r).OrgID, nil)
}

// handleImportAgentSkills — POST /api/v1/agents/{id}/skills/import.
func (s *Server) handleImportAgentSkills(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	s.importSkillArchive(w, r, agent.OrgID, &agent.ID)
}

func (s *Server) importSkillArchive(w http.ResponseWriter, r *http.Request, orgID uuid.UUID, agentID *uuid.UUID) {
	store, ok := s.skillsStore(w)
	if !ok {
		return
	}
	req, data, subdir, ok := s.readSkillArchive(w, r)
	if !ok {
		return
	}
	cands, err := skills.FromZip(data, subdir)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	if len(req.Names) > 0 {
		want := map[string]bool{}
		for _, n := range req.Names {
			if n = strings.TrimSpace(n); n != "" {
				want[n] = true
			}
		}
		var kept []skills.Candidate
		for _, c := range cands {
			if want[c.Spec.Name] {
				kept = append(kept, c)
				delete(want, c.Spec.Name)
			}
		}
		if len(want) > 0 {
			missing := make([]string, 0, len(want))
			for n := range want {
				missing = append(missing, n)
			}
			writeErr(w, http.StatusBadRequest, "not in the archive: "+strings.Join(missing, ", "))
			return
		}
		cands = kept
	}

	// What already exists at the target level. Without replace an existing
	// name is a problem — the import does not overwrite someone's work unasked.
	var existing []skills.Skill
	if agentID == nil {
		existing, err = store.ListLibrary(r.Context(), orgID)
	} else {
		existing, err = store.ListOwn(r.Context(), orgID, *agentID)
	}
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	taken := make(map[string]bool, len(existing))
	for _, sk := range existing {
		taken[sk.Name] = true
	}

	res := skillImportResult{DryRun: req.DryRun, Skills: make([]skillImportItem, 0, len(cands))}
	seen := map[string]bool{}
	invalid := false
	specs := make([]skills.Spec, 0, len(cands))
	for _, c := range cands {
		c.Spec.AgentID = agentID
		item := skillImportItem{Dir: c.Dir, Name: c.Spec.Name, Description: c.Spec.Description,
			License: c.Spec.License, Exists: taken[c.Spec.Name],
			Problems: append([]string{}, c.Problems...), Skipped: c.Skipped}
		for _, f := range c.Spec.Files {
			item.Files++
			if f.Binary() {
				item.BinaryFiles++
			}
		}
		if item.Exists && !req.Replace {
			item.Problems = append(item.Problems, "a skill with this name already exists — import with replace to overwrite it")
		}
		if seen[c.Spec.Name] {
			item.Problems = append(item.Problems, "the archive holds two skills of this name")
		}
		seen[c.Spec.Name] = true
		if len(item.Problems) > 0 {
			item.Status = "invalid"
			invalid = true
		} else {
			item.Status = "ready"
		}
		res.Skills = append(res.Skills, item)
		specs = append(specs, c.Spec)
	}
	if req.DryRun {
		writeJSON(w, http.StatusOK, res)
		return
	}
	if invalid {
		writeJSON(w, http.StatusUnprocessableEntity, res)
		return
	}
	stored, err := store.WriteAll(r.Context(), orgID, specs, req.Replace)
	if err != nil {
		mapSkillErr(w, err)
		return
	}
	for i := range res.Skills {
		res.Skills[i].ID = stored[i].ID.String()
		res.Skills[i].Status = "created"
		if res.Skills[i].Exists {
			res.Skills[i].Status = "replaced"
		}
	}
	res.Imported = len(stored)
	writeJSON(w, http.StatusOK, res)
}

// readSkillArchive reads either request shape and returns the archive bytes.
func (s *Server) readSkillArchive(w http.ResponseWriter, r *http.Request) (skillImportRequest, []byte, string, bool) {
	var req skillImportRequest
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if ct == "application/json" {
		if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.URL) == "" {
			writeErr(w, http.StatusBadRequest, `expected {"url": "…"} or a zip archive as the body`)
			return req, nil, "", false
		}
		data, subdir, err := s.SkillFetcher.Fetch(r.Context(), req.URL)
		if err != nil {
			mapSkillErr(w, err)
			return req, nil, "", false
		}
		return req, data, subdir, true
	}

	q := r.URL.Query()
	req.DryRun = truthy(q.Get("dry_run"))
	req.Replace = truthy(q.Get("replace"))
	if n := q.Get("names"); n != "" {
		req.Names = strings.Split(n, ",")
	}
	body := http.MaxBytesReader(w, r.Body, skills.MaxArchiveBytes)
	data, err := io.ReadAll(body)
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeErr(w, http.StatusRequestEntityTooLarge, "the archive is too large")
		return req, nil, "", false
	}
	if err != nil || len(data) == 0 {
		writeErr(w, http.StatusBadRequest, "expected a zip archive as the body")
		return req, nil, "", false
	}
	return req, data, q.Get("subdir"), true
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}
