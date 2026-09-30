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

/* The organisation's speech server (#497, #498).
 *
 * A call speaks an agent's replies with a voice synthesised on the device, or
 * with one from a speech server of the organisation's — an OpenAI-compatible
 * endpoint: POST /v1/audio/speech and, when an admin allows it,
 * POST /v1/audio/transcriptions. The first such server is educa AI: an
 * organisation that already holds an educa token for its engine speaks
 * through educa AI without further setup; its own base URL and key override
 * that. The app never reaches the server itself — the control plane holds
 * the key and hands back audio or text. No cloud synthesis billed per
 * character is wired in.
 */

// Bounds of one request.
const (
	synthesizeMaxRunes      = 1000
	synthesizeMaxBytes      = 20 << 20
	synthesizeTimeout       = 60 * time.Second
	synthesizeStreamTimeout = 3 * time.Minute
	instructionsMaxRunes    = 300
	speechServerURLMax      = 500
	// A call's turn is cut at 30 s; a minute of 16 kHz mono PCM16 is 1.92 MB.
	transcribeMaxBytes = 2 << 20
	transcribeTimeout  = 60 * time.Second
)

// The speed a server honours; outside it educa AI clamps anyway, and a
// sentence at double speed is not understood.
const (
	minSpeed = 0.70
	maxSpeed = 1.30
)

// Defaults of the OpenAI dialect educa AI speaks.
const (
	defaultSpeechVoice     = "DEFAULT_VOICE"
	defaultTranscribeModel = "whisper-1"
	educaDefaultBaseURL    = "https://api.educaai.de"
)

// The organisation secrets an educa AI endpoint is reached with — the ones
// the educa-ai engine uses (internal/daemon/runtime_educa.go). The contract
// seat first: it is paid for either way, the API token is billed per use.
var educaSecrets = []string{"educa_seat_token", "educa_api_token"}

// speechServerClient is the client the control plane speaks to the server
// with. No client timeout: a streamed answer takes as long as the speech, so
// every request carries its own deadline.
var speechServerClient = &http.Client{}

// newSynthLimiter caps requests per seat: 30 a minute is a busy call, and
// far below what a script looping over the endpoint would ask of the server.
func newSynthLimiter() *webhookLimiter {
	return &webhookLimiter{hits: map[string][]time.Time{}, maxHits: 30, window: time.Minute}
}

// speechEndpoint is the server a request goes to: the organisation's own, or
// its educa AI endpoint.
type speechEndpoint struct {
	settings org.SpeechServer
	base     string
	key      string
	// source is "own", "educa" or "" (none).
	source string
}

func (e speechEndpoint) ok() bool { return e.base != "" }

func (e speechEndpoint) transcribeModel() string {
	if e.settings.TranscribeModel != "" {
		return e.settings.TranscribeModel
	}
	return defaultTranscribeModel
}

func (e speechEndpoint) request(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
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

// speechEndpointOf resolves where the organisation's speech goes.
func (s *Server) speechEndpointOf(ctx context.Context, orgID uuid.UUID) (speechEndpoint, error) {
	var e speechEndpoint
	if s.Org == nil {
		return e, nil
	}
	st, err := s.Org.SpeechServer(ctx, orgID)
	if err != nil {
		return e, err
	}
	e.settings = st
	if st.Configured() {
		e.base, e.key, e.source = st.BaseURL, s.secretValue(ctx, orgID, org.SpeechServerKey), "own"
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

// speechServerView is what the settings show: never a key, only whether the
// own one is stored, and which server is in effect.
type speechServerView struct {
	BaseURL         string `json:"base_url"`
	Model           string `json:"model"`
	Voice           string `json:"voice"`
	KeySet          bool   `json:"key_set"`
	Transcribe      bool   `json:"transcribe"`
	TranscribeModel string `json:"transcribe_model"`
	// Effective is the server in use: the own one, educa AI, or none.
	Effective struct {
		Source  string `json:"source"`
		BaseURL string `json:"base_url"`
	} `json:"effective"`
}

func (s *Server) speechServerView(ctx context.Context, orgID uuid.UUID) (speechServerView, error) {
	e, err := s.speechEndpointOf(ctx, orgID)
	if err != nil {
		return speechServerView{}, err
	}
	v := speechServerView{BaseURL: e.settings.BaseURL, Model: e.settings.Model, Voice: e.settings.Voice,
		KeySet:     s.secretValue(ctx, orgID, org.SpeechServerKey) != "",
		Transcribe: e.settings.Transcribe, TranscribeModel: e.transcribeModel()}
	v.Effective.Source, v.Effective.BaseURL = e.source, e.base
	if v.Effective.Source == "" {
		v.Effective.Source = "none"
	}
	return v, nil
}

// handleGetSpeechServer: the organisation's speech server, without a key.
func (s *Server) handleGetSpeechServer(w http.ResponseWriter, r *http.Request) {
	v, err := s.speechServerView(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// normalizeSpeechServerURL accepts an http(s) address without query,
// fragment or credentials and drops a trailing slash and a trailing /v1 —
// the path the requests add themselves.
func normalizeSpeechServerURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if len(raw) > speechServerURLMax {
		return "", fmt.Errorf("the base URL is at most %d characters", speechServerURLMax)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", errors.New("the base URL is an http(s) address like https://speech.example.org, without query or credentials")
	}
	base := strings.TrimRight(u.String(), "/")
	base = strings.TrimSuffix(base, "/v1")
	return base, nil
}

// handleSetSpeechServer stores the settings; key, when given, goes into the
// organisation's secrets ("" removes it). An empty base URL removes the own
// server and its key — educa AI applies again where the organisation has it.
func (s *Server) handleSetSpeechServer(w http.ResponseWriter, r *http.Request) {
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
	base, err := normalizeSpeechServerURL(in.BaseURL)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	v := org.SpeechServer{BaseURL: base, Model: strings.TrimSpace(in.Model), Voice: strings.TrimSpace(in.Voice),
		Transcribe: in.Transcribe, TranscribeModel: strings.TrimSpace(in.TranscribeModel)}
	for _, n := range []string{v.Model, v.Voice, v.TranscribeModel} {
		if utf8.RuneCountInString(n) > voice.ServerNameMax {
			writeErr(w, http.StatusBadRequest, fmt.Sprintf("model and voice names are at most %d characters", voice.ServerNameMax))
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
	if err := s.Org.SetSpeechServer(r.Context(), p.OrgID, v); err != nil {
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
			if err := s.Secrets.Put(r.Context(), p.OrgID, org.SpeechServerKey, k); err != nil {
				mapErr(w, err)
				return
			}
			if err := s.Secrets.MarkSensitive(r.Context(), p.OrgID, org.SpeechServerKey); err != nil {
				mapErr(w, err)
				return
			}
		} else if s.secretValue(r.Context(), p.OrgID, org.SpeechServerKey) != "" {
			if err := s.Secrets.Delete(r.Context(), p.OrgID, org.SpeechServerKey); err != nil {
				mapErr(w, err)
				return
			}
		}
	}
	s.handleGetSpeechServer(w, r)
}

// clampSpeed turns a rate into the speed the server takes: 0 is omitted,
// the rest held to 0.70–1.30.
func clampSpeed(rate float64) (float64, bool) {
	if rate == 0 {
		return 0, false
	}
	if rate < minSpeed {
		rate = minSpeed
	}
	if rate > maxSpeed {
		rate = maxSpeed
	}
	return rate, true
}

// handleSynthesize speaks a text with the organisation's speech server. The
// app asks this in a call when the agent's voice names the server as its
// source; model and voice default to the organisation's, then to the
// server's own. Buffered, it answers WAV; with stream it passes the
// server's MP3 through as it arrives — educa AI's first bytes come after
// about half a second, so the app starts speaking before the reply is whole. Nothing is stored: the recording
// gets lengths, no text.
func (s *Server) handleSynthesize(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Text         string  `json:"text"`
		Model        string  `json:"model"`
		Voice        string  `json:"voice"`
		Rate         float64 `json:"rate"`
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
	text := strings.TrimSpace(in.Text)
	if n := utf8.RuneCountInString(text); n == 0 || n > synthesizeMaxRunes {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("text is required, at most %d characters", synthesizeMaxRunes))
		return
	}
	model, vname := strings.TrimSpace(in.Model), strings.TrimSpace(in.Voice)
	lang, instr := strings.TrimSpace(in.Language), strings.TrimSpace(in.Instructions)
	if utf8.RuneCountInString(model) > voice.ServerNameMax || utf8.RuneCountInString(vname) > voice.ServerNameMax {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("model and voice are at most %d characters", voice.ServerNameMax))
		return
	}
	if utf8.RuneCountInString(instr) > instructionsMaxRunes {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("instructions are at most %d characters", instructionsMaxRunes))
		return
	}
	if len(lang) > 35 || strings.ContainsAny(lang, " \t\n") {
		writeErr(w, http.StatusBadRequest, "language is a BCP 47 tag like de-DE")
		return
	}
	if in.Rate < 0 {
		writeErr(w, http.StatusBadRequest, "rate is positive, or 0 for the server's own")
		return
	}
	e, err := s.speechEndpointOf(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !e.ok() {
		writeErr(w, http.StatusConflict, "this organisation has no speech server")
		return
	}
	if !s.synthLimiter.allow(p.ID.String(), time.Now()) {
		w.Header().Set("Retry-After", "10")
		writeErr(w, http.StatusTooManyRequests, "too many syntheses in a minute")
		return
	}
	if model == "" {
		model = e.settings.Model
	}
	if vname == "" {
		vname = e.settings.Voice
	}
	if vname == "" {
		vname = defaultSpeechVoice
	}
	body := map[string]any{"input": text, "voice": vname, "response_format": "wav"}
	if model != "" {
		body["model"] = model
	}
	if lang != "" {
		body["language"] = lang
	}
	if instr != "" {
		body["instructions"] = instr
	}
	if speed, ok := clampSpeed(in.Rate); ok {
		body["speed"] = speed
	}
	timeout := synthesizeTimeout
	if in.Stream {
		body["response_format"], body["stream_format"] = "mp3", "audio"
		timeout = synthesizeStreamTimeout
	}
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	req, err := e.request(ctx, http.MethodPost, "/v1/audio/speech", bytes.NewReader(raw))
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the speech server's address is not usable")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	resp, err := speechServerClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the speech server did not answer")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("the speech server answered HTTP %d", resp.StatusCode))
		return
	}
	chars := utf8.RuneCountInString(text)
	if in.Stream {
		n := s.streamAudio(w, resp.Body)
		if n > 0 {
			s.recordSpeech(r.Context(), p.OrgID, in.Agent, "speech_synthesized",
				map[string]any{"chars": chars, "bytes": n, "ms": time.Since(started).Milliseconds(), "model": model, "stream": true})
		}
		return
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, synthesizeMaxBytes+1))
	switch {
	case err != nil:
		writeErr(w, http.StatusBadGateway, "the speech server's answer broke off")
		return
	case len(audio) > synthesizeMaxBytes:
		writeErr(w, http.StatusBadGateway, "the speech server's answer is larger than 20 MB")
		return
	case len(audio) == 0:
		writeErr(w, http.StatusBadGateway, "the speech server answered no audio")
		return
	}
	s.recordSpeech(r.Context(), p.OrgID, in.Agent, "speech_synthesized",
		map[string]any{"chars": chars, "bytes": len(audio), "ms": time.Since(started).Milliseconds(), "model": model})
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(audio)
}

// streamAudio passes the server's MP3 through, flushed chunk by chunk, so
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
					writeErr(w, http.StatusBadGateway, "the speech server answered no audio")
				} else {
					writeErr(w, http.StatusBadGateway, "the speech server's answer broke off")
				}
			}
			return total
		}
	}
	return total
}

// handleTranscribe recognises one turn of a call on the organisation's
// speech server (#498): the body is the turn as WAV, 16 kHz mono PCM16, at
// most a minute. Only where an admin has turned it on — the audio then
// leaves the device, to the organisation's own server. Nothing is stored.
func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	e, err := s.speechEndpointOf(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !e.ok() || !e.settings.Transcribe {
		writeErr(w, http.StatusConflict, "recognition on the speech server is off for this organisation")
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
		writeErr(w, http.StatusBadGateway, "the speech server's address is not usable")
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	started := time.Now()
	resp, err := speechServerClient.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the speech server did not answer")
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("the speech server answered HTTP %d", resp.StatusCode))
		return
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		writeErr(w, http.StatusBadGateway, "the speech server's answer is not readable")
		return
	}
	text := strings.TrimSpace(out.Text)
	s.recordSpeech(r.Context(), p.OrgID, r.URL.Query().Get("agent"), "speech_transcribed",
		map[string]any{"bytes": len(audio), "chars": utf8.RuneCountInString(text), "ms": time.Since(started).Milliseconds(), "model": e.transcribeModel()})
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// recordSpeech notes one request to the speech server: in the recording of
// the agent the app spoke with, when it named one of this organisation, and
// in the log. Lengths, durations and the model only — never text or audio.
func (s *Server) recordSpeech(ctx context.Context, orgID uuid.UUID, agent, kind string, payload map[string]any) {
	payload["source"] = "server"
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
