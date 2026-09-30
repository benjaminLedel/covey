package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"covey/internal/org"
	"covey/internal/voice"
)

/* The organisation's voice provider (#497, #498).
 *
 * A call speaks an agent's replies with one voice source: the
 * organisation's voice provider, an OpenAI-compatible speech server —
 * POST /v1/audio/speech and, when an admin allows it,
 * POST /v1/audio/transcriptions. The default is educa AI: an organisation
 * that already holds an educa token for its engine speaks through educa AI
 * without further setup; its own base URL and key override that. Without
 * either, the apps speak with the system's synthesis. The app never reaches
 * the provider itself — the control plane holds the key and hands back
 * audio or text.
 */

// Bounds of one request.
const (
	synthesizeMaxRunes      = 1000
	synthesizeMaxBytes      = 20 << 20
	synthesizeTimeout       = 60 * time.Second
	synthesizeStreamTimeout = 3 * time.Minute
	voiceProviderURLMax     = 500
	// A call's turn is cut at 30 s; a minute of 16 kHz mono PCM16 is 1.92 MB.
	transcribeMaxBytes = 2 << 20
	transcribeTimeout  = 60 * time.Second
	// The test of the settings waits no longer than a person would.
	voiceProviderTestTimeout = 20 * time.Second
)

// Defaults of the OpenAI dialect educa AI speaks.
const (
	defaultSpeechVoice     = "DEFAULT_VOICE"
	defaultTranscribeModel = "whisper-1"
	educaDefaultBaseURL    = "https://api.educaai.de"
)

// voiceProviderTestText is the sentence the settings' test synthesises.
const voiceProviderTestText = "This is a test of the voice provider."

// The organisation secrets an educa AI endpoint is reached with — the ones
// the educa-ai engine uses (internal/daemon/runtime_educa.go). The contract
// seat first: it is paid for either way, the API token is billed per use.
var educaSecrets = []string{"educa_seat_token", "educa_api_token"}

// voiceProviderClient is the client the control plane speaks to the
// provider with. No client timeout: a streamed answer takes as long as the
// speech, so every request carries its own deadline.
var voiceProviderClient = &http.Client{}

// newSynthLimiter caps requests per seat: 30 a minute is a busy call, and
// far below what a script looping over the endpoint would ask of the
// provider.
func newSynthLimiter() *webhookLimiter {
	return &webhookLimiter{hits: map[string][]time.Time{}, maxHits: 30, window: time.Minute}
}

// voiceProvider is where a request goes: the organisation's own server, or
// its educa AI endpoint.
type voiceProvider struct {
	settings org.VoiceProvider
	base     string
	key      string
	// source is "own", "educa" or "" (none).
	source string
}

func (e voiceProvider) ok() bool { return e.base != "" }

func (e voiceProvider) transcribeModel() string {
	if e.settings.TranscribeModel != "" {
		return e.settings.TranscribeModel
	}
	return defaultTranscribeModel
}

func (e voiceProvider) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, body)
	if err != nil {
		return nil, err
	}
	if e.key != "" {
		req.Header.Set("Authorization", "Bearer "+e.key)
	}
	return req, nil
}

// educaBaseURL is the educa AI endpoint the control plane reaches: the
// hosted one, or COVEY_EDUCA_BASE_URL — the variable the engine reads in
// the sandbox, read here in the control plane's own environment.
func educaBaseURL() string {
	if b := strings.TrimRight(strings.TrimSpace(os.Getenv("COVEY_EDUCA_BASE_URL")), "/"); b != "" {
		return b
	}
	return educaDefaultBaseURL
}

func (s *Server) secretValue(ctx context.Context, orgID uuid.UUID, key string) string {
	if s.Secrets == nil {
		return ""
	}
	v, err := s.Secrets.Value(ctx, orgID, key, 0)
	if err != nil {
		if v, err = s.Secrets.Get(ctx, orgID, key); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(v)
}

// voiceProviderOf resolves where the organisation's speech goes.
func (s *Server) voiceProviderOf(ctx context.Context, orgID uuid.UUID) (voiceProvider, error) {
	var e voiceProvider
	if s.Org == nil {
		return e, nil
	}
	st, err := s.Org.VoiceProvider(ctx, orgID)
	if err != nil {
		return e, err
	}
	e.settings = st
	if st.Configured() {
		e.base, e.key, e.source = st.BaseURL, s.secretValue(ctx, orgID, org.VoiceProviderKey), "own"
		return e, nil
	}
	for _, name := range educaSecrets {
		if k := s.secretValue(ctx, orgID, name); k != "" {
			e.base, e.key, e.source = educaBaseURL(), k, "educa"
			return e, nil
		}
	}
	return e, nil
}

// voiceProviderView is what the settings show: never a key, only whether
// the own one is stored, and which provider is in effect.
type voiceProviderView struct {
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	Voice           string `json:"voice"`
	KeySet          bool   `json:"key_set"`
	Transcribe      bool   `json:"transcribe"`
	TranscribeModel string `json:"transcribe_model"`
	// Effective is the provider in use: the own server, educa AI, or none.
	Effective struct {
		Source  string `json:"source"`
		BaseURL string `json:"base_url"`
	} `json:"effective"`
}

func (s *Server) voiceProviderView(ctx context.Context, orgID uuid.UUID) (voiceProviderView, error) {
	e, err := s.voiceProviderOf(ctx, orgID)
	if err != nil {
		return voiceProviderView{}, err
	}
	v := voiceProviderView{BaseURL: e.settings.BaseURL, Model: e.settings.Model, Voice: e.settings.Voice,
		KeySet:     s.secretValue(ctx, orgID, org.VoiceProviderKey) != "",
		Transcribe: e.settings.Transcribe, TranscribeModel: e.transcribeModel()}
	v.Effective.Source, v.Effective.BaseURL = e.source, e.base
	if v.Effective.Source == "" {
		v.Effective.Source = "none"
	}
	return v, nil
}

// handleGetVoiceProvider: the organisation's voice provider, without a key.
func (s *Server) handleGetVoiceProvider(w http.ResponseWriter, r *http.Request) {
	v, err := s.voiceProviderView(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// normalizeVoiceProviderURL accepts an http(s) address without query,
// fragment or credentials and drops a trailing slash and a trailing /v1 —
// the path the requests add themselves.
func normalizeVoiceProviderURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > voiceProviderURLMax {
		return "", fmt.Errorf("the base URL is at most %d characters", voiceProviderURLMax)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("the base URL is an http(s) address like https://speech.example.org, without query or credentials")
	}
	base := strings.TrimRight(u.String(), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base, nil
}

// handleSetVoiceProvider stores the settings; key, when given, goes into
// the organisation's secrets ("" removes it). An empty base URL removes the
// own server and its key — educa AI applies again where the organisation
// has it.
func (s *Server) handleSetVoiceProvider(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		BaseURL         string  `json:"base_url"`
		Model           string  `json:"model"`
		Voice           string  `json:"voice"`
		Key             *string `json:"key"`
		Transcribe      bool    `json:"transcribe"`
		TranscribeModel string  `json:"transcribe_model"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	base, err := normalizeVoiceProviderURL(in.BaseURL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v := org.VoiceProvider{BaseURL: base, Model: strings.TrimSpace(in.Model), Voice: strings.TrimSpace(in.Voice),
		Transcribe: in.Transcribe, TranscribeModel: strings.TrimSpace(in.TranscribeModel)}
	for _, n := range []string{v.Model, v.Voice, v.TranscribeModel} {
		if utf8.RuneCountInString(n) > voice.ProviderNameMax {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("model and voice names are at most %d characters", voice.ProviderNameMax))
			return
		}
	}
	if v.TranscribeModel == defaultTranscribeModel {
		v.TranscribeModel = ""
	}
	if in.Key != nil && len(*in.Key) > 4000 {
		writeErr(w, http.StatusBadRequest, "the key is at most 4000 characters")
		return
	}
	if s.Org == nil || s.Secrets == nil {
		writeErr(w, http.StatusServiceUnavailable, "settings are not available")
		return
	}
	if err := s.Org.SetVoiceProvider(r.Context(), p.OrgID, v); err != nil {
		mapErr(w, err)
		return
	}
	key := in.Key
	if !v.Configured() {
		empty := ""
		key = &empty
	}
	if key != nil {
		if k := strings.TrimSpace(*key); k != "" {
			if err := s.Secrets.Put(r.Context(), p.OrgID, org.VoiceProviderKey, k); err != nil {
				mapErr(w, err)
				return
			}
			if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, org.VoiceProviderKey); err != nil {
				mapErr(w, err)
				return
			}
		} else if s.secretValue(r.Context(), p.OrgID, org.VoiceProviderKey) != "" {
			if err := s.Secrets.Delete(r.Context(), p.OrgID, org.VoiceProviderKey); err != nil {
				mapErr(w, err)
				return
			}
		}
	}
	s.handleGetVoiceProvider(w, r)
}

// clampSpeed turns a speed into the one the provider takes: 0 is omitted,
// the rest held to what it honours.
func clampSpeed(speed float64) (float64, bool) {
	if speed == 0 {
		return 0, false
	}
	return min(max(speed, voice.MinSpeed), voice.MaxSpeed), true
}

// speechRequest is one synthesis at the provider.
type speechRequest struct {
	Text, Voice, Language, Instructions string
	Speed                               float64
	Stream                              bool
}

// speak asks the provider for speech. The answer is the provider's 200
// response, whose body the caller closes; an error is a short message for
// the app, nothing of the provider's own answer.
func (e voiceProvider) speak(ctx context.Context, in speechRequest) (*http.Response, error) {
	vname := in.Voice
	if vname == "" {
		vname = e.settings.Voice
	}
	if vname == "" {
		vname = defaultSpeechVoice
	}
	body := map[string]any{"input": in.Text, "voice": vname, "response_format": "wav"}
	if e.settings.Model != "" {
		body["model"] = e.settings.Model
	}
	if in.Language != "" {
		body["language"] = in.Language
	}
	if in.Instructions != "" {
		body["instructions"] = in.Instructions
	}
	if speed, ok := clampSpeed(in.Speed); ok {
		body["speed"] = speed
	}
	if in.Stream {
		body["response_format"], body["stream_format"] = "mp3", "audio"
	}
	raw, _ := json.Marshal(body)
	req, err := e.request(ctx, http.MethodPost, "/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		return nil, errors.New("the voice provider's address is not usable")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := voiceProviderClient.Do(req)
	if err != nil {
		return nil, errors.New("the voice provider did not answer")
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return nil, fmt.Errorf("the voice provider answered HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// readAudio reads a buffered answer, bounded.
func readAudio(body io.Reader) ([]byte, error) {
	audio, err := io.ReadAll(io.LimitReader(body, synthesizeMaxBytes+1))
	switch {
	case err != nil:
		return nil, errors.New("the voice provider's answer broke off")
	case len(audio) > synthesizeMaxBytes:
		return nil, errors.New("the voice provider's answer is larger than 20 MB")
	case len(audio) == 0:
		return nil, errors.New("the voice provider answered no audio")
	}
	return audio, nil
}

// handleTestVoiceProvider synthesises one sentence with the saved settings
// and says whether it worked: {ok, ms, bytes} or {ok: false, error}. The
// audio is dropped — the test is for whoever sets the provider up, not a
// way to fetch speech.
func (s *Server) handleTestVoiceProvider(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	e, err := s.voiceProviderOf(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	fail := func(msg string) { writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg}) }
	if !e.ok() {
		fail("no voice provider is set")
		return
	}
	if !s.synthLimiter.allow(p.ID.String(), time.Now()) {
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, "too many syntheses in a minute")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), voiceProviderTestTimeout)
	defer cancel()
	started := time.Now()
	resp, err := e.speak(ctx, speechRequest{Text: voiceProviderTestText, Language: "en"})
	if err != nil {
		fail(err.Error())
		return
	}
	defer resp.Body.Close()
	audio, err := readAudio(resp.Body)
	if err != nil {
		fail(err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ms": time.Since(started).Milliseconds(), "bytes": len(audio)})
}

// handleSynthesize speaks a text with the organisation's voice provider:
// the Mac app in a call, and the voice page's preview. The voice defaults
// to the organisation's, then to the provider's own; the model is always
// the organisation's. Buffered, it answers WAV; with stream it passes the
// provider's MP3 through as it arrives — educa AI's first bytes come after
// about half a second, so the app starts speaking before the reply is
// whole. Nothing is stored: the recording gets lengths, no text.
func (s *Server) handleSynthesize(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Text         string  `json:"text"`
		Voice        string  `json:"voice"`
		Speed        float64 `json:"speed"`
		Language     string  `json:"language"`
		Instructions string  `json:"instructions"`
		Stream       bool    `json:"stream"`
		// Agent, when the app names the agent it speaks for, attributes the
		// call in that agent's recording.
		Agent string `json:"agent"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	req := speechRequest{
		Text: strings.TrimSpace(in.Text), Voice: strings.TrimSpace(in.Voice), Speed: in.Speed,
		Language: strings.TrimSpace(in.Language), Instructions: strings.TrimSpace(in.Instructions), Stream: in.Stream,
	}
	if n := utf8.RuneCountInString(req.Text); n == 0 || n > synthesizeMaxRunes {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("text is required, at most %d characters", synthesizeMaxRunes))
		return
	}
	if utf8.RuneCountInString(req.Voice) > voice.ProviderNameMax {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("the voice is at most %d characters", voice.ProviderNameMax))
		return
	}
	if utf8.RuneCountInString(req.Instructions) > voice.InstructionsMax {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("instructions are at most %d characters", voice.InstructionsMax))
		return
	}
	if len(req.Language) > 35 || strings.ContainsAny(req.Language, " \t\n") {
		writeErr(w, http.StatusBadRequest, "language is a BCP 47 tag like de-DE")
		return
	}
	if req.Speed < 0 {
		writeErr(w, http.StatusBadRequest, "speed is positive, or 0 for the voice's own")
		return
	}
	e, err := s.voiceProviderOf(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !e.ok() {
		writeErr(w, http.StatusConflict, "this organisation has no voice provider")
		return
	}
	if !s.synthLimiter.allow(p.ID.String(), time.Now()) {
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, "too many syntheses in a minute")
		return
	}
	timeout := synthesizeTimeout
	if req.Stream {
		timeout = synthesizeStreamTimeout
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	started := time.Now()
	resp, err := e.speak(ctx, req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()
	chars := utf8.RuneCountInString(req.Text)
	if req.Stream {
		n := s.streamAudio(w, resp.Body)
		if n > 0 {
			s.recordSpeech(r.Context(), p.OrgID, in.Agent, "speech_synthesized",
				map[string]any{"chars": chars, "bytes": n, "ms": time.Since(started).Milliseconds(), "model": e.settings.Model, "stream": true})
		}
		return
	}
	audio, err := readAudio(resp.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordSpeech(r.Context(), p.OrgID, in.Agent, "speech_synthesized",
		map[string]any{"chars": chars, "bytes": len(audio), "ms": time.Since(started).Milliseconds(), "model": e.settings.Model})
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

// streamAudio passes the provider's MP3 through, flushed chunk by chunk, so
// the app can start speaking with the first sentence. Until the first byte
// has come, a failure is still a 502; after it the stream just ends. It
// returns the bytes passed.
func (s *Server) streamAudio(w http.ResponseWriter, body io.Reader) int {
	buf := make([]byte, 16<<10)
	rc := http.NewResponseController(w)
	total := 0
	started := false
	for total < synthesizeMaxBytes {
		n, err := body.Read(buf)
		if n > 0 {
			if !started {
				started = true
				h := w.Header()
				h.Set("Content-Type", "audio/mpeg")
				h.Set("Cache-Control", "no-store")
				w.WriteHeader(http.StatusOK)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return total
			}
			total += n
			_ = rc.Flush()
		}
		if err != nil {
			if !started {
				if errors.Is(err, io.EOF) {
					writeErr(w, http.StatusBadGateway, "the voice provider answered no audio")
				} else {
					writeErr(w, http.StatusBadGateway, "the voice provider's answer broke off")
				}
			}
			return total
		}
	}
	return total
}

// handleTranscribe recognises one turn of a call at the organisation's
// voice provider (#498): the body is the turn as WAV, 16 kHz mono PCM16, at
// most a minute. Only where an admin has turned it on — the audio then
// leaves the device, to the organisation's provider. Nothing is stored.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	e, err := s.voiceProviderOf(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !e.ok() || !e.settings.Transcribe {
		writeErr(w, http.StatusConflict, "recognition at the voice provider is off for this organisation")
		return
	}
	lang := strings.TrimSpace(r.URL.Query().Get("language"))
	if len(lang) > 35 || strings.ContainsAny(lang, " \t\n") {
		writeErr(w, http.StatusBadRequest, "language is a BCP 47 tag like de-DE")
		return
	}
	audio, err := io.ReadAll(io.LimitReader(r.Body, transcribeMaxBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if len(audio) > transcribeMaxBytes {
		writeErr(w, http.StatusRequestEntityTooLarge, "at most a minute of audio, 2 MB")
		return
	}
	if len(audio) < 44 || string(audio[:4]) != "RIFF" || string(audio[8:12]) != "WAVE" {
		writeErr(w, http.StatusBadRequest, "the body is a WAV file")
		return
	}
	if !s.synthLimiter.allow("transcribe:"+p.ID.String(), time.Now()) {
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, "too many recognitions in a minute")
		return
	}
	var form bytes.Buffer
	mw := multipart.NewWriter(&form)
	fw, _ := mw.CreateFormFile("file", "turn.wav")
	_, _ = fw.Write(audio)
	_ = mw.WriteField("model", e.transcribeModel())
	_ = mw.WriteField("response_format", "json")
	if lang != "" {
		// Whisper takes the language as ISO 639-1: the tag's first part.
		base, _, _ := strings.Cut(lang, "-")
		_ = mw.WriteField("language", strings.ToLower(base))
	}
	_ = mw.Close()
	ctx, cancel := context.WithTimeout(r.Context(), transcribeTimeout)
	defer cancel()
	req, err := e.request(ctx, http.MethodPost, "/v1/audio/transcriptions", &form)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the voice provider's address is not usable")
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	started := time.Now()
	resp, err := voiceProviderClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the voice provider did not answer")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("the voice provider answered HTTP %d", resp.StatusCode))
		return
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadGateway, "the voice provider's answer is not readable")
		return
	}
	text := strings.TrimSpace(out.Text)
	s.recordSpeech(r.Context(), p.OrgID, r.URL.Query().Get("agent"), "speech_transcribed",
		map[string]any{"bytes": len(audio), "chars": utf8.RuneCountInString(text), "ms": time.Since(started).Milliseconds(), "model": e.transcribeModel()})
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// recordSpeech notes one request to the voice provider: in the recording of
// the agent the app spoke with, when it named one of this organisation, and
// in the log. Lengths, durations and the model only — never text or audio.
func (s *Server) recordSpeech(ctx context.Context, orgID uuid.UUID, agent, kind string, payload map[string]any) {
	payload["source"] = "provider"
	if s.Log != nil {
		args := []any{"org", orgID}
		for k, v := range payload {
			args = append(args, k, v)
		}
		s.Log.Info(strings.ReplaceAll(kind, "_", " "), args...)
	}
	id, err := uuid.Parse(agent)
	if err != nil || s.Obs == nil || s.Registry == nil {
		return
	}
	a, err := s.Registry.Get(ctx, id)
	if err != nil || a.OrgID != orgID {
		return
	}
	_ = s.Obs.Record(ctx, orgID, id, nil, kind, payload)
}
