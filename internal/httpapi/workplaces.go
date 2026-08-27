package httpapi

// The workplaces an agent can be put in (spec/16): the profiles the project
// publishes, plus the ones this organisation brings along itself — and for each
// of them this instance's image and whether it lies ready on a runner.
//
// Two sources, one list, because for whoever chooses one they are the same
// thing: a name that says what an agent works in. Where it comes from is worth
// showing (Kind), not worth splitting the list over.
//
// What the interface must not do is invent this list. It used to offer "own
// image" as a free-text field on the agent, and that cost three things: the
// image appeared in no overview, it carried no description saying what it was
// for, and a typo in it only failed at the next wake. An own workplace is
// therefore created HERE, once, and chosen afterwards like any other.

import (
	"errors"
	"net/http"
	"strings"

	"covey/internal/agents"
	"covey/internal/sandbox"
	"covey/internal/workplaces"
)

type workplaceView struct {
	sandbox.Profile
	// Kind: "catalog" for a profile of the project's catalogue, "own" for one
	// this organisation created. It decides what may be done with it — an own
	// one can be deleted, a published one cannot.
	Kind string `json:"kind"`
	// Source says where the IMAGE behind it comes from: "catalog" (published
	// for this Covey version), "env" (this instance named it) or "builtin" (the
	// compiled default). Empty for an own workplace, where the organisation
	// named the image itself and there is nothing to disambiguate.
	Source string `json:"source,omitempty"`
	// Tag is the name the image was published under ("base-v0.4.0"). The
	// reference in Image is pinned by digest and therefore unreadable; this is
	// the same image in the form a human compares with a release note.
	Tag string `json:"tag,omitempty"`
	// Platforms is what the published manifest carries.
	Platforms []string `json:"platforms,omitempty"`
	// Available: the image lies on at least one runner of this organisation.
	// Nil = nobody could be asked (no runner connected, or none answered) —
	// which is something different from "not there" and must not be shown as
	// such.
	Available *bool `json:"available,omitempty"`
	// Agents are the agents of THIS organisation working in it — named, not
	// counted: whoever is about to change or delete a workplace wants to know
	// whom it concerns.
	Agents []agents.AgentRef `json:"agents,omitempty"`
	// Provides is what the image says about itself: the same file the agent
	// reads inside its sandbox (internal/sandbox/workplaces). Without it,
	// "which workplace do I put this agent in" is answerable only by reading a
	// Dockerfile — and an agent that cannot see its tools fetches them a second
	// time (#102).
	//
	// Empty for an own workplace: there the organisation named the image, and
	// what is in it, the platform does not know.
	Provides *sandbox.WorkplaceDoc `json:"provides,omitempty"`
}

func (s *Server) handleListWorkplaces(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)

	// Dieselbe Reihenfolge, die auch die Sandbox startet: Umgebung, dann der
	// veroeffentlichte Katalog, dann die kompilierte Voreinstellung. Die
	// Ansicht soll zeigen, was gilt — nicht, was in einer der drei Quellen
	// steht.
	var env, catalogue map[string]string
	if s.Config != nil {
		env = s.Config.SandboxImageEnv
	}
	var eintraege map[string]sandbox.CatalogImage
	if s.Workplaces != nil {
		eintraege = s.Workplaces.Resolved(r.Context())
		catalogue = map[string]string{}
		for name, img := range eintraege {
			catalogue[name] = img.Ref
		}
	}
	images := sandbox.Resolve(env, catalogue)

	var eigene []workplaces.Workplace
	if s.OrgWorkplaces != nil {
		eigene, _ = s.OrgWorkplaces.List(r.Context(), p.OrgID)
	}

	byAgent, _ := s.Registry.AgentsPerWorkplace(r.Context(), p.OrgID)

	out := make([]workplaceView, 0, len(sandbox.All())+len(eigene))
	var report []string
	for _, prof := range sandbox.All() {
		quelle := "builtin"
		if img := catalogue[prof.Name]; img != "" {
			quelle = "catalog"
		}
		if img := env[prof.Name]; img != "" {
			quelle = "env"
		}
		if img := images[prof.Name]; img != "" {
			prof.Image = img
		}
		view := workplaceView{Profile: prof, Kind: "catalog", Source: quelle,
			Agents: byAgent[prof.Name]}
		if doc, ok := sandbox.Workplace(prof.Name); ok {
			view.Provides = &doc
		}
		if img, ok := eintraege[prof.Name]; ok && quelle == "catalog" {
			view.Tag, view.Platforms = img.Tag, img.Platforms
		}
		if prof.Default {
			// An agent without a choice works here, and that is the majority of
			// them — otherwise the default would look unused.
			view.Agents = append(view.Agents, byAgent[""]...)
		}
		out = append(out, view)
		report = append(report, prof.Image)
	}
	for _, own := range eigene {
		out = append(out, workplaceView{
			Profile: sandbox.Profile{
				Name: own.Name, Label: own.Label,
				Description: own.Description, Image: own.Image,
			},
			Kind:   "own",
			Agents: byAgent[own.Name],
		})
		report = append(report, own.Image)
	}

	// Ob ein Image bereitliegt, beantwortet der Runner: Es liegt dort, wo die
	// Sandbox startet, nicht dort, wo die Control Plane laeuft.
	if s.RunnerPool != nil {
		present := s.RunnerPool.WorkplaceImages(r.Context(), p.OrgID, report)
		for i := range out {
			if ok, asked := present[out[i].Image]; asked {
				out[i].Available = &ok
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleCreateWorkplace registers an image of this organisation's own as a
// workplace: a name, what it is for, and the reference.
func (s *Server) handleCreateWorkplace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if s.OrgWorkplaces == nil {
		writeErr(w, http.StatusServiceUnavailable, "no workplace store")
		return
	}
	var in struct {
		Name        string `json:"name"`
		Label       string `json:"label"`
		Description string `json:"description"`
		Image       string `json:"image"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	name := strings.ToLower(strings.TrimSpace(in.Name))
	// Ein Name aus dem Katalog ist vergeben, auch wenn diese Organisation ihn
	// noch nie benutzt hat: Sonst haette ein Agent auf `dev` zwei Bedeutungen,
	// und welche gilt, entschiede die Reihenfolge einer Schleife.
	if _, ok := sandbox.Get(name); ok {
		writeErr(w, http.StatusConflict, "the name "+name+" belongs to a published workplace")
		return
	}
	created, err := s.OrgWorkplaces.Create(r.Context(), p.OrgID, p.ID, workplaces.Workplace{
		Name: name, Label: strings.TrimSpace(in.Label),
		Description: strings.TrimSpace(in.Description), Image: strings.TrimSpace(in.Image),
	})
	switch {
	case errors.Is(err, workplaces.ErrTaken):
		writeErr(w, http.StatusConflict, "this organisation already has a workplace called "+name)
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleDeleteWorkplace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if s.OrgWorkplaces == nil {
		writeErr(w, http.StatusServiceUnavailable, "no workplace store")
		return
	}
	err := s.OrgWorkplaces.Delete(r.Context(), p.OrgID, r.PathValue("name"))
	switch {
	case errors.Is(err, workplaces.ErrNotFound):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, workplaces.ErrInUse):
		// Loeschen wuerde die Agenten auf einen Namen zeigen lassen, hinter dem
		// nichts mehr steht — auffallen wuerde es beim naechsten Wecken.
		writeErr(w, http.StatusConflict, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handlePullWorkplace fetches a workplace's image onto the organisation's
// runners now, instead of leaving it to the first wake.
//
// Nothing about this changes configuration — it procures what the instance
// would start anyway. What it buys is that the wait is visible: several
// gigabytes in front of the first agent of the day look like a hanging wake,
// and this is the same download with somebody watching it.
func (s *Server) handlePullWorkplace(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	if _, ok := sandbox.Get(name); !ok {
		if s.OrgWorkplaces == nil {
			writeErr(w, http.StatusNotFound, "no such workplace")
			return
		}
		if _, err := s.OrgWorkplaces.Get(r.Context(), p.OrgID, name); err != nil {
			writeErr(w, http.StatusNotFound, "no such workplace")
			return
		}
	}
	if s.RunnerPool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no runner pool")
		return
	}
	image, problems, err := s.RunnerPool.PullWorkplace(r.Context(), p.OrgID, name)
	if err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"image":    image,
		"problems": problems,
	})
}
