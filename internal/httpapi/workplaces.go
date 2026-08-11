package httpapi

// The workplaces an agent can be put in (spec/16): the catalogue from
// internal/sandbox, plus this instance's image for each and the answer to
// whether that image lies ready on a runner.
//
// The list comes from the catalogue for the same reason the target systems'
// does: a list the interface keeps itself is a second list, and the third
// profile would be missing from it. What the interface cannot know is the last
// part — an image is available where the sandbox starts, and that is the
// runner, not the browser.

import (
	"net/http"

	"covey/internal/sandbox"
)

type workplaceView struct {
	sandbox.Profile
	// Available: the image lies on at least one runner of this organisation.
	// Nil = nobody could be asked (no runner connected, or none answered) —
	// which is something different from "not there" and must not be shown as
	// such.
	Available *bool `json:"available,omitempty"`
	// InUse is the number of this organisation's agents working in it.
	InUse int `json:"in_use"`
}

func (s *Server) handleListWorkplaces(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	profiles := sandbox.All()

	images := map[string]string{}
	if s.Config != nil {
		images = s.Config.SandboxImages
	}

	var present map[string]bool
	if s.RunnerPool != nil {
		present = s.RunnerPool.Workplaces(r.Context(), p.OrgID)
	}

	inUse, _ := s.Registry.SandboxImagesInUseForOrg(r.Context(), p.OrgID)

	out := make([]workplaceView, 0, len(profiles))
	for _, prof := range profiles {
		if img := images[prof.Name]; img != "" {
			prof.Image = img
		}
		view := workplaceView{Profile: prof, InUse: inUse[prof.Name]}
		if prof.Default {
			// An agent without a choice works here, and that is the majority of
			// them — otherwise the default would look unused.
			view.InUse += inUse[""]
		}
		if ok, asked := present[prof.Image]; asked {
			view.Available = &ok
		}
		out = append(out, view)
	}
	writeJSON(w, http.StatusOK, out)
}
