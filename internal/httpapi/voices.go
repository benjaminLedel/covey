package httpapi

// Voices: an author's style as an object of the organisation (spec/24).
//
// The shape follows the skills library beside it — a library page, objects that
// belong to the organisation, an assignment per agent — with one difference
// that decides the endpoints: a voice is BUILT. Texts are uploaded, a build
// measures them, and one of the four artefacts is written by a model and has to
// be released by a person before it acts anywhere.
//
// Permissions as for skills: every role may read (a voice is a description of
// how to write, not a secret), the manage roles may change. Releasing a card is
// a manage action too — it is the moment a description of somebody's hand
// starts appearing in every prompt of every agent that carries it.

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/llm"
	"covey/internal/voice"
)

// voiceStore answers the not-configured case itself, as skillsStore does.
func (s *Server) voiceStore(w http.ResponseWriter) (*voice.Store, bool) {
	if s.Voices == nil {
		writeErr(w, http.StatusServiceUnavailable, "voices are not configured on this instance")
		return nil, false
	}
	return s.Voices, true
}

func (s *Server) handleListVoices(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	list, err := store.List(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []voice.Voice{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateVoice(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	var in struct {
		Name     string `json:"name"`
		Language string `json:"language"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	v, err := store.Create(r.Context(), principalFrom(r).OrgID, in.Name, in.Language)
	switch {
	case errors.Is(err, voice.ErrExists):
		writeErr(w, http.StatusConflict, err.Error())
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusCreated, v)
	}
}

// voiceView is one voice with its corpus — what the detail page needs in one
// request.
type voiceView struct {
	voice.Voice
	Corpus []voice.StoredDocument `json:"corpus"`
	// Tone is what an agent would carry: the rendered TONE.md. Shown because
	// the artefacts on their own do not say what actually reaches a prompt, and
	// that is the question somebody has in front of a voice.
	Tone string `json:"tone"`
}

func (s *Server) handleGetVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	corpus, err := store.Documents(r.Context(), v.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if corpus == nil {
		corpus = []voice.StoredDocument{}
	}
	writeJSON(w, http.StatusOK, voiceView{Voice: v, Corpus: corpus, Tone: voice.Render(v)})
}

func (s *Server) handleDeleteVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	if err := store.Delete(r.Context(), principalFrom(r).OrgID, v.ID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAddVoiceDocument(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
		Body string `json:"body"`
		Kind string `json:"kind"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	d, err := store.AddDocument(r.Context(), principalFrom(r).OrgID, v.ID, in.Name, in.Body, in.Kind)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusCreated, d)
	}
}

func (s *Server) handleDeleteVoiceDocument(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	docID, err := uuid.Parse(r.PathValue("docID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid document id")
		return
	}
	if err := store.DeleteDocument(r.Context(), principalFrom(r).OrgID, v.ID, docID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleBuildVoice measures the corpus and derives the artefacts.
//
// The card is the one part that costs money, and it is the one part that can be
// left out: without a control-plane credential the build still produces
// profile, exemplars and contrast, and says why there is no card. A feature
// that refuses everything because one quarter of it needs a model would make
// the other three quarters unreachable for an installation that has none.
func (s *Server) handleBuildVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	orgID := principalFrom(r).OrgID
	corpus, err := store.Corpus(ctx, v.ID, voice.KindAuthor)
	if err != nil {
		mapErr(w, err)
		return
	}
	if len(corpus) == 0 {
		writeErr(w, http.StatusBadRequest,
			"this voice has no texts yet — a voice is built from what its author wrote")
		return
	}
	reference, err := store.Corpus(ctx, v.ID, voice.KindReference)
	if err != nil {
		mapErr(w, err)
		return
	}
	built, err := voice.Build(corpus, v.Language, voice.Reference(reference))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	card := ""
	if s.Secrets != nil {
		if provider, err := llm.Resolve(ctx, s.Secrets, orgID); err == nil {
			if card, err = voice.Card(ctx, provider, built, v.Name); err != nil {
				s.Log.Warn("voice: the card could not be written", "voice", v.ID, "err", err)
				built.Notes = append(built.Notes,
					"the card could not be written: "+err.Error()+" — the measured artefacts stand")
			}
		} else {
			built.Notes = append(built.Notes, "no control-plane credential, so no card was written — "+
				"bands, exemplars and contrast are measured and stand without it")
		}
	}
	updated, err := store.SaveBuild(ctx, orgID, v.ID, built, card)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// handleReleaseVoiceCard makes a card the one that acts.
func (s *Server) handleReleaseVoiceCard(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Card string `json:"card"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if in.Card == "" {
		in.Card = v.Card // release what the build proposed, unchanged
	}
	p := principalFrom(r)
	updated, err := store.Release(r.Context(), p.OrgID, v.ID, p.ID, in.Card)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, updated)
	}
}

// handleSetAgentVoice puts an agent on a voice, or takes it off one.
//
// Assigning writes the TONE.md into the agent's config — a config version like
// any other, so the change is visible, reviewable and revertible where every
// other change to an agent is (spec/02). That is the whole reason the object in
// the table does not reach into a running agent: what acts is a file the agent
// carries, and nobody has to guess which version of a voice produced it.
func (s *Server) handleSetAgentVoice(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	var in struct {
		VoiceID string `json:"voice_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	p := principalFrom(r)
	ctx := r.Context()

	// Taking a voice off: the link goes, the TONE.md stays. Removing it would
	// change how the agent writes as a side effect of a picker — and the file
	// is a config version somebody can revert on its own page.
	if in.VoiceID == "" {
		if err := store.SetAgentVoice(ctx, agent.ID, nil); err != nil {
			mapErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true,
			"note": "the TONE.md stays in the config — remove it there if the agent should write without a voice"})
		return
	}

	id, err := uuid.Parse(in.VoiceID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid voice_id")
		return
	}
	v, err := store.Get(ctx, p.OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !v.BuiltOK() {
		writeErr(w, http.StatusBadRequest,
			"this voice has not been built yet — there is nothing to write into the agent's TONE.md")
		return
	}
	cfg, err := s.Registry.CurrentConfig(ctx, agent.ID)
	files := map[string]string{}
	if err == nil {
		for name, content := range cfg.Files {
			files[name] = content
		}
	}
	files["TONE.md"] = voice.Render(v)
	if _, err := s.Registry.SaveConfig(ctx, agent.ID, files, &p.ID); err != nil {
		mapErr(w, err)
		return
	}
	if err := store.SetAgentVoice(ctx, agent.ID, &v.ID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "voice": v.Name})
}

// requireVoice resolves {id} within the caller's organisation.
func (s *Server) requireVoice(w http.ResponseWriter, r *http.Request) (*voice.Store, voice.Voice, bool) {
	store, ok := s.voiceStore(w)
	if !ok {
		return nil, voice.Voice{}, false
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return nil, voice.Voice{}, false
	}
	v, err := store.Get(r.Context(), principalFrom(r).OrgID, id)
	if err != nil {
		mapErr(w, err)
		return nil, voice.Voice{}, false
	}
	return store, v, true
}
