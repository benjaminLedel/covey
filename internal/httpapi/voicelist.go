package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/voice"
)

/* The voice provider's named voices (#518).
 *
 * educa AI lists the voices its /v1/audio/speech takes at
 * GET /api/tts/voices, per language and with a name a person reads. The
 * control plane reads that list with the credentials it speaks with, keeps
 * it an hour per organisation, and offers it to the settings as a choice.
 * A provider that does not answer the path — any other OpenAI-compatible
 * server — has no list: listed is false and the settings keep their free
 * text field.
 *
 * The list is also what an agent's spoken voice is chosen from when its
 * covey voice names none: among the voices of the call's language, one per
 * agent, spread over the organisation's agents in the order they were hired
 * so that two of them sound different where the list allows
 * (assignSpokenVoice).
 */

const (
	// voiceListTTL is how long a read list is kept per organisation.
	voiceListTTL = time.Hour
	// voiceListFailTTL is how long a failure is kept: short, so a provider
	// that was down is asked again soon, long enough that a busy call page
	// does not ask it on every request.
	voiceListFailTTL = time.Minute
	voiceListTimeout = 5 * time.Second
	voiceListMaxBody = 1 << 20
	voiceListMax     = 500
)

// The rules a call's spoken voice comes from (voice_source).
const (
	// spokenFromVoice: the covey voice names it (speech.voice).
	spokenFromVoice = "voice"
	// spokenAssigned: chosen for the agent from the provider's list.
	spokenAssigned = "assigned"
	// spokenProviderDefault: the default — the organisation's configured
	// default voice, else the one the provider's list names, else whatever
	// the provider speaks when asked for none.
	spokenProviderDefault = "provider-default"
)

// providerVoice is one voice the provider lists.
type providerVoice struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Language    string `json:"language"`
}

// providerVoiceList is what GET /org/voice-provider/voices answers.
type providerVoiceList struct {
	// Listed says the provider answered a list; false means free text.
	Listed bool `json:"listed"`
	// Default is the provider's own default voice, when it names one.
	Default *providerVoice  `json:"default"`
	Voices  []providerVoice `json:"voices"`
}

// voiceListCache keeps each organisation's list for an hour. The zero value
// is ready to use.
type voiceListCache struct {
	mu      sync.Mutex
	entries map[uuid.UUID]voiceListEntry
}

type voiceListEntry struct {
	// fingerprint is the provider the list came from: another address or
	// key reads it anew.
	fingerprint string
	list        providerVoiceList
	until       time.Time
}

func (c *voiceListCache) get(org uuid.UUID, fp string, now time.Time) (providerVoiceList, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[org]
	if !ok || e.fingerprint != fp || now.After(e.until) {
		return providerVoiceList{}, false
	}
	return e.list, true
}

func (c *voiceListCache) put(org uuid.UUID, e voiceListEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[uuid.UUID]voiceListEntry{}
	}
	c.entries[org] = e
}

// forget drops an organisation's list: its provider settings changed.
func (c *voiceListCache) forget(org uuid.UUID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, org)
}

// fingerprint names the provider a list is read from, without holding the
// key itself.
func (e voiceProvider) fingerprint() string {
	sum := sha256.Sum256([]byte(e.key))
	return e.base + "|" + hex.EncodeToString(sum[:8])
}

// voices reads the provider's list. A provider without one — 404, another
// status, an answer that is not the list — is not an error: listed is false.
// The error is only for a provider that could not be reached, so the caller
// keeps that for a shorter time.
func (e voiceProvider) voices(ctx context.Context) (providerVoiceList, error) {
	out := providerVoiceList{Voices: []providerVoice{}}
	if !e.ok() {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, voiceListTimeout)
	defer cancel()
	req, err := e.request(ctx, http.MethodGet, "/api/tts/voices", nil)
	if err != nil {
		return out, nil
	}
	req.Header.Set("Accept", "application/json")
	resp, err := voiceProviderClient.Do(req)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return out, nil
	}
	var in struct {
		Default *providerVoice  `json:"default"`
		Voices  []providerVoice `json:"voices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, voiceListMaxBody)).Decode(&in); err != nil || in.Voices == nil {
		return out, nil
	}
	seen := map[string]bool{}
	for _, v := range in.Voices {
		v = cleanProviderVoice(v)
		if v.Name == "" || seen[v.Name] || len(out.Voices) >= voiceListMax {
			continue
		}
		seen[v.Name] = true
		out.Voices = append(out.Voices, v)
	}
	slices.SortFunc(out.Voices, func(a, b providerVoice) int {
		if c := strings.Compare(a.Language, b.Language); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	if in.Default != nil {
		if d := cleanProviderVoice(*in.Default); d.Name != "" {
			if d.DisplayName == d.Name {
				for _, v := range out.Voices {
					if v.Name == d.Name {
						d.DisplayName = v.DisplayName
					}
				}
			}
			out.Default = &d
		}
	}
	out.Listed = true
	return out, nil
}

// cleanProviderVoice trims a listed voice and drops what a synthesis
// request could not take.
func cleanProviderVoice(v providerVoice) providerVoice {
	v.Name = strings.TrimSpace(v.Name)
	if len([]rune(v.Name)) > voice.ProviderNameMax {
		v.Name = ""
	}
	v.DisplayName = strings.TrimSpace(v.DisplayName)
	if v.DisplayName == "" || len([]rune(v.DisplayName)) > voice.ProviderNameMax {
		v.DisplayName = v.Name
	}
	v.Language = baseLanguage(v.Language)
	return v
}

// baseLanguage is a tag's first part, lower case: de for de-DE.
func baseLanguage(tag string) string {
	base, _, _ := strings.Cut(strings.TrimSpace(tag), "-")
	base, _, _ = strings.Cut(base, "_")
	if len(base) > 8 {
		return ""
	}
	return strings.ToLower(base)
}

// voiceListOf is the organisation's list, from the cache when it is fresh.
func (s *Server) voiceListOf(ctx context.Context, orgID uuid.UUID, e voiceProvider) providerVoiceList {
	if !e.ok() {
		return providerVoiceList{Voices: []providerVoice{}}
	}
	now := time.Now()
	fp := e.fingerprint()
	if l, ok := s.voiceLists.get(orgID, fp, now); ok {
		return l
	}
	l, err := e.voices(ctx)
	ttl := voiceListTTL
	if err != nil {
		ttl = voiceListFailTTL
		if s.Log != nil {
			s.Log.Info("voice list not read", "org", orgID, "reason", err)
		}
	}
	s.voiceLists.put(orgID, voiceListEntry{fingerprint: fp, list: l, until: now.Add(ttl)})
	return l
}

// handleGetVoiceProviderVoices: the provider's named voices, for every
// member — the voice page and the agent's settings show them.
func (s *Server) handleGetVoiceProviderVoices(w http.ResponseWriter, r *http.Request) {
	orgID := principalFrom(r).OrgID
	e, err := s.voiceProviderOf(r.Context(), orgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.voiceListOf(r.Context(), orgID, e))
}

// spokenVoice is the voice at the provider a call speaks an agent's words
// with, and the rule that chose it.
type spokenVoice struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Language    string `json:"language"`
	Source      string `json:"source"`
}

// assignSpokenVoice chooses an agent's spoken voice from the provider's
// list when its covey voice names none: among the voices of the language,
// by the agent's place in orgAgents — the organisation's hired agents in the
// order they were hired (hiredAgentIDs). So the choice stays the same from
// call to call, agents one after the other get different voices until the
// language's voices are used up, and hiring another agent does not move
// the voices of those already there. An agent missing from orgAgents (a
// draft) takes the place after them. Without a voice of the language, the
// default: the one the organisation configured for its provider, else the
// one the provider's list names.
func assignSpokenVoice(list providerVoiceList, lang string, agentID uuid.UUID, orgAgents []uuid.UUID, configured string) spokenVoice {
	lang = baseLanguage(lang)
	if lang == "" && list.Default != nil {
		lang = list.Default.Language
	}
	var pool []providerVoice
	for _, v := range list.Voices {
		if v.Language == lang {
			pool = append(pool, v)
		}
	}
	if len(pool) > 0 {
		i := slices.Index(orgAgents, agentID)
		if i < 0 {
			i = len(orgAgents)
		}
		v := pool[i%len(pool)]
		return spokenVoice{Name: v.Name, DisplayName: v.DisplayName, Language: v.Language, Source: spokenAssigned}
	}
	out := spokenVoice{Source: spokenProviderDefault}
	switch {
	case configured != "":
		return lookupSpoken(list, configured, spokenProviderDefault)
	case list.Default != nil:
		out.Name, out.DisplayName, out.Language = list.Default.Name, list.Default.DisplayName, list.Default.Language
	}
	return out
}

// lookupSpoken fills the display name and language of a named voice from
// the list, when the list has it.
func lookupSpoken(list providerVoiceList, name, source string) spokenVoice {
	out := spokenVoice{Name: name, DisplayName: name, Source: source}
	for _, v := range list.Voices {
		if v.Name == name {
			out.DisplayName, out.Language = v.DisplayName, v.Language
		}
	}
	return out
}

// resolveSpokenVoice is the voice at the provider an agent speaks with in
// a call: the covey voice's own (speech.voice), else one assigned from the
// provider's list for the call's language, else the provider's default.
// lang is the call's language; empty takes the covey voice's, then the
// provider default's.
func (s *Server) resolveSpokenVoice(ctx context.Context, orgID, agentID uuid.UUID, own *voice.Speech, lang string) spokenVoice {
	e, err := s.voiceProviderOf(ctx, orgID)
	if err != nil || !e.ok() {
		if own != nil && own.Voice != "" {
			return spokenVoice{Name: own.Voice, DisplayName: own.Voice, Source: spokenFromVoice}
		}
		return spokenVoice{Source: spokenProviderDefault}
	}
	list := s.voiceListOf(ctx, orgID, e)
	if own != nil && own.Voice != "" {
		return lookupSpoken(list, own.Voice, spokenFromVoice)
	}
	var ids []uuid.UUID
	if s.Registry != nil {
		if all, err := s.Registry.List(ctx, orgID); err == nil {
			ids = hiredAgentIDs(all)
		}
	}
	return assignSpokenVoice(list, lang, agentID, ids, e.settings.Voice)
}

// hiredAgentIDs are the agents a voice is spread over, in the order they
// were hired (the id breaks a tie): a new hire comes last and moves nobody,
// and a draft does not take a voice away from an agent that already speaks.
func hiredAgentIDs(all []agents.Agent) []uuid.UUID {
	hired := make([]agents.Agent, 0, len(all))
	for _, a := range all {
		if a.HiredAt != nil {
			hired = append(hired, a)
		}
	}
	slices.SortFunc(hired, func(a, b agents.Agent) int {
		if c := a.HiredAt.Compare(*b.HiredAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID.String(), b.ID.String())
	})
	ids := make([]uuid.UUID, len(hired))
	for i, a := range hired {
		ids[i] = a.ID
	}
	return ids
}

// handleAgentSpokenVoice says which voice at the provider the agent speaks
// with in a call and why — for the agent's settings. It resolves as a call
// would with nobody particular on the line: the chat occasion's voice of
// the agent, its department or the organisation (#471). ?lang= names the
// call's language.
func (s *Server) handleAgentSpokenVoice(w http.ResponseWriter, r *http.Request) {
	agent, ok := s.requireAgent(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	lang := r.URL.Query().Get("lang")
	var own *voice.Speech
	out := map[string]any{"voice": nil}
	if s.Voices != nil {
		if c := s.Voices.Resolve(ctx, agent.OrgID, agent.ID, voice.OccasionChat, voice.Audience{}); c.Found() {
			if v, err := s.Voices.Get(ctx, agent.OrgID, c.VoiceID); err == nil {
				own = v.Speech
				out["voice"] = map[string]any{"id": v.ID, "name": v.Name}
				if lang == "" {
					lang = v.Language
				}
			}
		}
	}
	e, _ := s.voiceProviderOf(ctx, agent.OrgID)
	out["provider"] = e.ok()
	out["spoken"] = s.resolveSpokenVoice(ctx, agent.OrgID, agent.ID, own, lang)
	writeJSON(w, http.StatusOK, out)
}
