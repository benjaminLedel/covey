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
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/llm"
	"covey/internal/observability"
	"covey/internal/style"
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
		Purpose  string `json:"purpose"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	v, err := store.CreateWith(r.Context(), principalFrom(r).OrgID,
		voice.Draft{Name: in.Name, Language: in.Language, Purpose: in.Purpose})
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
	// Checks say what the author corpus lacks, while it is collected (#458).
	Checks []voice.Check `json:"checks"`
	// Assignable: whether an agent can carry the voice yet.
	Assignable bool `json:"assignable"`
	// UsedBy: who names the voice for what — agents, departments and the
	// organisation's defaults, per occasion (#471).
	UsedBy []voice.Use `json:"used_by"`
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
	texts, err := store.Corpus(r.Context(), v.ID, voice.KindAuthor)
	if err != nil {
		mapErr(w, err)
		return
	}
	usedBy, err := store.UsedBy(r.Context(), v.OrgID, v.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, voiceView{Voice: v, Corpus: corpus, Tone: voice.Render(v),
		Checks: voice.CheckCorpus(texts, v.Language), Assignable: v.Assignable(), UsedBy: usedBy})
}

// handlePatchVoice changes what a voice is for.
func (s *Server) handlePatchVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Purpose string `json:"purpose"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	updated, err := store.SetPurpose(r.Context(), principalFrom(r).OrgID, v.ID, in.Purpose)
	writeVoiceResult(w, updated, err)
}

// writeVoiceResult answers a change to a voice: 400 for the caller's mistake,
// the voice otherwise.
func writeVoiceResult(w http.ResponseWriter, v voice.Voice, err error) {
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, v)
	}
}

// voiceModel is the organisation's model for the calls that write a voice. No
// credential is a state, not a fault: the answer says so, and the page says
// the same before anybody clicks.
func (s *Server) voiceModel(w http.ResponseWriter, r *http.Request) (llm.Provider, bool) {
	if s.OrgLLM == nil && s.Secrets == nil {
		writeErr(w, http.StatusServiceUnavailable, llm.ErrNoCredential.Error())
		return nil, false
	}
	provider, err := s.resolveOrgLLM(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, llm.ErrNoCredential.Error())
		return nil, false
	}
	return provider, true
}

// handleDescribeVoice writes a voice from a description (#458): card,
// exemplars and a suggested chat tone, in one call of the organisation's
// model. The result is a draft — the card and the exemplars act once a person
// releases them, the tone once somebody saves it.
//
// A measured voice is not described over: its exemplars are quotes and its
// profile is what the gate checks, and a description would replace evidence
// with a claim. Its card is refined instead.
func (s *Server) handleDescribeVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Description string `json:"description"`
		Language    string `json:"language"`
		Purpose     string `json:"purpose"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if v.Source == voice.FromTexts && v.Measured() {
		writeErr(w, http.StatusConflict, "this voice is measured from texts — refine its card instead of describing it")
		return
	}
	in.Description = strings.TrimSpace(in.Description)
	if in.Description == "" {
		in.Description = v.Description // write it again from what is stored
	}
	if in.Description == "" {
		writeErr(w, http.StatusBadRequest, "a described voice needs a description")
		return
	}
	if n := len([]rune(in.Description)); n > voice.DescriptionMax {
		writeErr(w, http.StatusBadRequest, "the description is longer than the limit of a few paragraphs")
		return
	}
	ctx := r.Context()
	orgID := principalFrom(r).OrgID
	if strings.TrimSpace(in.Purpose) != "" {
		var err error
		if v, err = store.SetPurpose(ctx, orgID, v.ID, in.Purpose); err != nil {
			writeVoiceResult(w, v, err)
			return
		}
	}
	lang := strings.TrimSpace(in.Language)
	if lang == "" {
		lang = v.Language
	}
	if lang == "" {
		lang = style.DetectLanguage(in.Description)
	}
	provider, ok := s.voiceModel(w, r)
	if !ok {
		return
	}
	d, err := voice.Describe(ctx, provider, voice.DescribeInput{Name: v.Name, Purpose: v.Purpose,
		Language: lang, Description: in.Description})
	if err != nil {
		if errors.Is(err, voice.ErrInvalid) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		s.Log.Warn("voice: the description could not be written", "voice", v.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "the model's answer could not be used: "+err.Error())
		return
	}
	updated, err := store.SaveDescribed(ctx, orgID, v.ID, in.Description, lang, d)
	writeVoiceResult(w, updated, err)
}

// handlePreviewVoice writes one short sample in a voice, on a topic a person
// picks, and stores nothing (#458). version "draft" (the default) hears the
// card and exemplars waiting for release, "released" the ones that act — the
// before and after of a refinement.
//
// A manage action although it changes nothing: it costs a model call, and
// the people who build a voice are the ones who need to hear it.
func (s *Server) handlePreviewVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Topic   string `json:"topic"`
		Kind    string `json:"kind"`
		Version string `json:"version"`
		// Audience is a department id (#471): the sample is written to
		// somebody of it, with its "how to speak with us" line.
		Audience string `json:"audience"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	card, exemplars := v.ReleasedCard, v.Exemplars
	switch in.Version {
	case "", "draft":
		in.Version = "draft"
		if strings.TrimSpace(v.Card) != "" {
			card = v.Card
		}
		if len(v.DraftExemplars) > 0 {
			exemplars = v.DraftExemplars
		}
	case "released":
	default:
		writeErr(w, http.StatusBadRequest, `version is "draft" or "released"`)
		return
	}
	audience := ""
	if in.Audience != "" {
		deptID, err := uuid.Parse(in.Audience)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "audience is a department id")
			return
		}
		aud, err := store.DepartmentAudience(r.Context(), principalFrom(r).OrgID, deptID)
		if err != nil {
			writeVoiceResult(w, voice.Voice{}, err)
			return
		}
		audience = voice.AudiencePrompt(voice.Choose(voice.OccasionChat, aud, nil, nil).Notes)
	}
	provider, ok := s.voiceModel(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	org, _ := store.OrgChatTone(ctx, principalFrom(r).OrgID)
	kind := in.Kind
	if kind == "" {
		kind = voice.DefaultKind(v.Purpose)
	}
	text, err := voice.Preview(ctx, provider, voice.PreviewInput{Name: v.Name, Language: v.Language,
		Purpose: v.Purpose, Card: card, Exemplars: exemplars,
		ChatTone: voice.EffectiveChatTone(v.ChatTone, org), Topic: in.Topic, Kind: kind, Audience: audience})
	if errors.Is(err, voice.ErrInvalid) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.Log.Warn("voice: the preview could not be written", "voice", v.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "the preview could not be written: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": text, "kind": kind, "version": in.Version,
		"provider": provider.Name()})
}

// handleRefineVoice revises the card by an instruction ("less formal, more
// concrete numbers"), and the exemplars of a described voice with it (#458).
// The revision is a DRAFT: the released card acts until a person releases the
// new one, as after any build. The answer carries the text before, so the
// page can put the two side by side.
func (s *Server) handleRefineVoice(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Instruction string `json:"instruction"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	// The revision starts from the draft where there is one — a second
	// instruction builds on the first — and from the released card otherwise.
	card := v.Card
	if strings.TrimSpace(card) == "" {
		card = v.ReleasedCard
	}
	described := v.Source == voice.FromDescription
	exemplars := v.DraftExemplars
	if len(exemplars) == 0 {
		exemplars = v.Exemplars
	}
	provider, ok := s.voiceModel(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	refined, err := voice.Refine(ctx, provider, voice.RefineInput{Name: v.Name, Language: v.Language,
		Purpose: v.Purpose, Card: card, Exemplars: exemplars, Instruction: in.Instruction, Described: described})
	if errors.Is(err, voice.ErrInvalid) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		s.Log.Warn("voice: the refinement could not be written", "voice", v.ID, "err", err)
		writeErr(w, http.StatusBadGateway, "the model's answer could not be used: "+err.Error())
		return
	}
	updated, err := store.SaveRefined(ctx, principalFrom(r).OrgID, v.ID, refined.Card, refined.Exemplars)
	if err != nil {
		mapErr(w, err)
		return
	}
	before := map[string]any{"card": card}
	if described {
		before["exemplars"] = exemplars
	}
	writeJSON(w, http.StatusOK, map[string]any{"voice": updated, "before": before})
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

	// The corrections go into the card prompt: a pair shows the hand where a
	// rule only describes it (spec/24). Best effort — a voice without pairs is
	// the ordinary case, and a failure to read them must not lose the build.
	pairs, err := store.Corrections(ctx, v.ID, 20)
	if err != nil {
		s.Log.Warn("voice: corrections not readable", "voice", v.ID, "err", err)
	}
	built.Purpose = v.Purpose
	card := ""
	if s.Secrets != nil || s.OrgLLM != nil {
		if provider, err := s.resolveOrgLLM(ctx, orgID); err == nil {
			if card, err = voice.Card(ctx, provider, built, v.Name, pairs); err != nil {
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

// handleSetVoiceChatTone sets how the agents carrying a voice talk in the
// team chat (#457). The same roles as every other change to a voice.
func (s *Server) handleSetVoiceChatTone(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in voice.ChatTone
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	updated, err := store.SetChatTone(r.Context(), principalFrom(r).OrgID, v.ID, in)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, updated)
	}
}

// handleSetVoiceSpeech sets how the agents carrying a voice sound when a
// call speaks their words through the voice provider (#497): the voice's
// name there, a style hint and the speed. An empty body clears it.
func (s *Server) handleSetVoiceSpeech(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in *voice.Speech
	if err := readJSON(r, &in); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if in == nil {
		in = &voice.Speech{}
	}
	updated, err := store.SetSpeech(r.Context(), principalFrom(r).OrgID, v.ID, *in)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, updated)
	}
}

// handleConversationSpeech says how an agent of the conversation sounds in
// a call (#497): the voice the #471 rule chooses for the chat occasion —
// the caller's department, then the agent's chat slot, then the
// organisation's — and that voice's speech, which the app passes to the
// voice provider. instructions is always set: the voice's own style hint,
// else one derived from the chat tone in effect. address is du, sie or ""
// — how the call greets (#506).
//
// speech.voice is the voice at the provider every sentence of the call is
// spoken with — replies, greeting, fillers and goodbye alike (#518): the
// covey voice's own, else one assigned to the agent from the provider's
// list for the call's language (?lang=, else the covey voice's, else the
// provider default's), else the default. voice_source says which rule
// chose it, spoken_voice names it for a person. Null speech is only left
// where nothing names a voice: the provider speaks its default.
func (s *Server) handleConversationSpeech(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	agentID, err := uuid.Parse(r.URL.Query().Get("agent"))
	if err != nil || !c.Has(chat.Ref{Kind: chat.MemberAgent, ID: agentID}) {
		writeErr(w, http.StatusNotFound, "no such agent in this conversation")
		return
	}
	lang := r.URL.Query().Get("lang")
	if len(lang) > 35 {
		writeErr(w, http.StatusBadRequest, "lang is a BCP 47 tag like de-DE")
		return
	}
	out := map[string]any{"speech": nil, "voice": nil, "level": voice.LevelNone, "instructions": voice.SpeechInstructions(voice.ChatTone{}, ""), "address": ""}
	ctx := r.Context()
	var own *voice.Speech
	if s.Voices != nil {
		// How it is spoken, for a provider that takes instructions: from the
		// chat tone in effect, also without a chat voice.
		choice, tone := s.callVoice(r, c, agentID)
		out["instructions"] = voice.SpeechInstructions(tone, "")
		// The address the call's greeting takes (#506), before the person has
		// said anything it could follow.
		out["address"] = tone.SpokenAddress()
		if choice.Found() {
			v, err := s.Voices.Get(ctx, c.OrgID, choice.VoiceID)
			if err != nil {
				mapErr(w, err)
				return
			}
			out["level"] = choice.Level
			out["voice"] = map[string]any{"id": v.ID, "name": v.Name}
			out["instructions"] = voice.SpeechInstructions(tone, v.Language)
			if lang == "" {
				lang = v.Language
			}
			if v.Speech != nil {
				own = v.Speech
				if v.Speech.Instructions != "" {
					out["instructions"] = v.Speech.Instructions
				}
			}
		}
	}
	spoken := s.resolveSpokenVoice(ctx, c.OrgID, agentID, own, lang)
	out["voice_source"] = spoken.Source
	out["spoken_voice"] = spoken
	if own != nil || spoken.Name != "" {
		sp := voice.Speech{}
		if own != nil {
			sp = *own
		}
		sp.Voice = spoken.Name
		out["speech"] = sp
	}
	writeJSON(w, http.StatusOK, out)
}

// callVoice is the voice and the chat tone an agent speaks with in a call
// in this conversation: the chat occasion's voice for the caller's
// audience (#471), and the tone in effect also without one. The speech
// settings and the greeting (#513) read the same.
func (s *Server) callVoice(r *http.Request, c chat.Conversation, agentID uuid.UUID) (voice.Choice, voice.ChatTone) {
	ctx := r.Context()
	aud := s.Voices.ConversationAudience(ctx, c.OrgID, c.ID, principalFrom(r).Email)
	choice := s.Voices.Resolve(ctx, c.OrgID, agentID, voice.OccasionChat, aud)
	return choice, s.Voices.ChatToneFor(ctx, c.OrgID, agentID, choice)
}

// handleGetOrgChatTone / handleSetOrgChatTone: the organisation's default
// tone in the team chat, for agents without a voice and for what a voice
// leaves open (#457). Read by every role, like the triage switch beside it;
// set by whoever manages the organisation.
func (s *Server) handleGetOrgChatTone(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	t, err := store.OrgChatTone(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleSetOrgChatTone(w http.ResponseWriter, r *http.Request) {
	store, ok := s.voiceStore(w)
	if !ok {
		return
	}
	var in voice.ChatTone
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	t, err := store.SetOrgChatTone(r.Context(), principalFrom(r).OrgID, in)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusOK, t)
	}
}

// handleSetAgentVoice puts an agent on a voice, or takes it off one — the
// one-voice assignment from before #471, which now fills the agent's
// customers and publications slots (SetAgentVoice). The slots themselves are
// PUT /agents/{id}/voices (voiceslots.go).
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
	if !v.Assignable() {
		msg := "this voice has not been built yet — there is nothing to write into the agent's TONE.md"
		if v.Source == voice.FromDescription {
			msg = "this voice is described and not released yet — everything in it was written by a model, " +
				"so it acts only once a person has released it"
		}
		writeErr(w, http.StatusBadRequest, msg)
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

// handleListVoiceCorrections: the pairs of a voice, newest first.
func (s *Server) handleListVoiceCorrections(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	list, err := store.Corrections(r.Context(), v.ID, 200)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []voice.Correction{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAddVoiceCorrection takes a pair from outside.
//
// The gate fills this by itself (see handleDecideApproval), and the endpoint
// exists for the other half of spec/24: somebody edits a published text in a
// target system, and the plugin that notices it posts the pair here. That half
// lives in the plugin pack, which is exactly why it needs a way in that does
// not require a change to covey.
func (s *Server) handleAddVoiceCorrection(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	var in struct {
		Before  string `json:"before"`
		After   string `json:"after"`
		Action  string `json:"action"`
		Source  string `json:"source"`
		AgentID string `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	p := principalFrom(r)
	c := voice.Correction{Before: in.Before, After: in.After, Action: in.Action,
		Source: in.Source, By: p.ID.String()}
	if in.AgentID != "" {
		id, err := uuid.Parse(in.AgentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent_id")
			return
		}
		c.AgentID = &id
	}
	out, err := store.AddCorrection(r.Context(), p.OrgID, v.ID, c)
	switch {
	case errors.Is(err, voice.ErrInvalid):
		writeErr(w, http.StatusBadRequest, err.Error())
	case err != nil:
		mapErr(w, err)
	default:
		writeJSON(w, http.StatusCreated, out)
	}
}

func (s *Server) handleDeleteVoiceCorrection(w http.ResponseWriter, r *http.Request) {
	store, v, ok := s.requireVoice(w, r)
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("correctionID"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid correction id")
		return
	}
	if err := store.DeleteCorrection(r.Context(), principalFrom(r).OrgID, v.ID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// approvalText is the agent's own text inside an approval — the prose the style
// gate would have measured, found the same way so that the two cannot disagree.
//
// The floor of 20 words is deliberately below the gate's default: what a
// reviewer bothers to rewrite is worth keeping as a pair even when it was too
// short to be measured.
func approvalText(appr observability.Approval) string {
	return style.ProseIn(appr.Params, 20)
}

// noteCorrection stores what a reviewer changed about an agent's text, against
// the voice that agent carries.
//
// Best effort by design: an agent without a voice has nowhere to put the pair,
// and a correction that cannot be stored must not hold up the approval that was
// the point of the click.
func (s *Server) noteCorrection(ctx context.Context, appr observability.Approval, before, after, by string) {
	if s.Voices == nil || strings.TrimSpace(before) == "" {
		return
	}
	voiceID, ok := s.Voices.VoiceOfAgent(ctx, appr.AgentID)
	if !ok {
		return
	}
	agentID := appr.AgentID
	if _, err := s.Voices.AddCorrection(ctx, appr.OrgID, voiceID, voice.Correction{
		AgentID: &agentID, Source: voice.SourceApproval, Action: appr.Action,
		Before: before, After: after, By: by,
	}); err != nil && !errors.Is(err, voice.ErrInvalid) {
		s.Log.Warn("voice: correction not stored", "approval", appr.ID, "err", err)
	}
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
