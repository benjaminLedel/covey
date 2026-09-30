package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeVoiceProvider is an OpenAI-compatible speech server in the shape educa
// AI speaks: /v1/audio/speech buffered (wav) or streamed (mp3, raw chunks)
// and /v1/audio/transcriptions.
type fakeVoiceProvider struct {
	*httptest.Server
	mu        sync.Mutex
	speech    []map[string]any
	auth      []string
	forms     []map[string]string
	fileSizes []int
	fail      bool
	// chunks the streamed answer is sent in; after the first it waits for
	// gate, so a test sees the first arrive before the rest is sent.
	chunks [][]byte
	gate   chan struct{}
}

var fakeWAV = []byte("RIFF\x24\x00\x00\x00WAVEfmt fake audio")

func newFakeVoiceProvider(t *testing.T) *fakeVoiceProvider {
	f := &fakeVoiceProvider{chunks: [][]byte{[]byte("chunk-one|"), []byte("chunk-two|"), []byte("chunk-three")}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		fail, gate := f.fail, f.gate
		f.mu.Unlock()
		if fail {
			http.Error(w, "upstream broke", http.StatusInternalServerError)
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/speech":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.mu.Lock()
			f.speech = append(f.speech, body)
			f.mu.Unlock()
			if body["stream_format"] == "audio" {
				w.Header().Set("Content-Type", "audio/mpeg")
				for i, c := range f.chunks {
					_, _ = w.Write(c)
					w.(http.Flusher).Flush()
					if i == 0 && gate != nil {
						select {
						case <-gate:
						case <-time.After(5 * time.Second):
						}
					}
				}
				return
			}
			w.Header().Set("Content-Type", "audio/wav")
			_, _ = w.Write(fakeWAV)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/transcriptions":
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			mr := multipart.NewReader(r.Body, params["boundary"])
			form := map[string]string{}
			size := 0
			for {
				part, err := mr.NextPart()
				if err != nil {
					break
				}
				data, _ := io.ReadAll(part)
				if part.FormName() == "file" {
					size = len(data)
					form["filename"] = part.FileName()
				} else {
					form[part.FormName()] = string(data)
				}
			}
			f.mu.Lock()
			f.forms = append(f.forms, form)
			f.fileSizes = append(f.fileSizes, size)
			f.mu.Unlock()
			_, _ = w.Write([]byte(`{"text":"  wie weit ist der Export  "}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeVoiceProvider) setFail(on bool) {
	f.mu.Lock()
	f.fail = on
	f.mu.Unlock()
}

func (f *fakeVoiceProvider) lastSpeech() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.speech[len(f.speech)-1]
}

func (f *fakeVoiceProvider) lastAuth() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.auth[len(f.auth)-1]
}

// wavOf is a WAV of n bytes of silence, the shape a call's turn has.
func wavOf(n int) []byte {
	b := append([]byte("RIFF\x00\x00\x00\x00WAVEfmt "), make([]byte, 32)...)
	return append(b, make([]byte, n)...)
}

// TestTheVoiceProviderSpeaksAndHears walks #497's and #498's one voice
// source: an organisation holding an educa AI token speaks through educa AI
// without further setup, its own server and key override that, the requests
// reach the provider in the OpenAI shape with the key as a bearer —
// buffered and streamed, with the speed held to what the provider honours
// —, the settings' test says whether it speaks without handing out audio,
// recognition works only once an admin turns it on, and no key is in any
// answer.
func TestTheVoiceProviderSpeaksAndHears(t *testing.T) {
	const educaKey, ownKey = "educa-seat-geheim-0815", "sk-own-geheim-4711"
	educa, own := newFakeVoiceProvider(t), newFakeVoiceProvider(t)
	t.Setenv("COVEY_EDUCA_BASE_URL", educa.URL)

	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "ada@test.local", "Ada", "controlling", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	var bodies []string
	keep := func(raw []byte) { bodies = append(bodies, string(raw)) }
	post := func(c *apiClient, path string, body any, want int) (*http.Response, []byte) {
		t.Helper()
		resp := c.do(http.MethodPost, path, body)
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		keep(raw)
		if resp.StatusCode != want {
			t.Fatalf("POST %s: HTTP %d, want %d: %s", path, resp.StatusCode, want, raw)
		}
		return resp, raw
	}
	expect := func(c *apiClient, method, path string, body any, want int) map[string]any {
		t.Helper()
		m := c.expect(method, path, body, want)
		raw, _ := json.Marshal(m)
		keep(raw)
		return m
	}
	synth := "/api/v1/speech/synthesize"
	test := "/api/v1/org/voice-provider/test"

	// No server and no educa token: nothing to speak with.
	post(ada, synth, map[string]any{"text": "Hallo."}, http.StatusConflict)
	if m := expect(admin, http.MethodPost, test, nil, http.StatusOK); m["ok"] != false || m["error"] == "" {
		t.Fatalf("test without a provider: %v", m)
	}
	expect(ada, http.MethodPost, test, nil, http.StatusForbidden)
	if m := expect(ada, http.MethodGet, "/api/v1/speech/model", nil, http.StatusOK); m["synthesize"] != false || m["transcribe"] != false {
		t.Fatalf("flags without a server: %v", m)
	}

	// The educa token the engine uses is enough.
	expect(admin, http.MethodPut, "/api/v1/secrets/educa_seat_token", map[string]string{"value": educaKey}, http.StatusOK)
	view := expect(admin, http.MethodGet, "/api/v1/org/voice-provider", nil, http.StatusOK)
	if eff := view["effective"].(map[string]any); eff["source"] != "educa" || eff["base_url"] != educa.URL || view["key_set"] != false {
		t.Fatalf("educa as the default: %v", view)
	}
	if m := expect(ada, http.MethodGet, "/api/v1/speech/model", nil, http.StatusOK); m["synthesize"] != true || m["transcribe"] != false {
		t.Fatalf("flags with educa: %v", m)
	}
	resp, audio := post(ada, synth, map[string]any{"text": "  Der Export läuft.  ", "speed": 2.5, "language": "de-DE",
		"instructions": "casual and friendly, concise"}, http.StatusOK)
	if resp.Header.Get("Content-Type") != "audio/wav" || !bytes.Equal(audio, fakeWAV) {
		t.Fatalf("buffered audio: %q %q", resp.Header.Get("Content-Type"), audio)
	}
	got := educa.lastSpeech()
	if got["input"] != "Der Export läuft." || got["voice"] != "DEFAULT_VOICE" || got["response_format"] != "wav" ||
		got["speed"] != 1.3 || got["language"] != "de-DE" || got["instructions"] != "casual and friendly, concise" {
		t.Fatalf("request to educa: %v", got)
	}
	if _, has := got["model"]; has {
		t.Fatalf("no model named, none sent: %v", got)
	}
	if _, has := got["stream_format"]; has {
		t.Fatalf("a buffered request names no stream format: %v", got)
	}
	if educa.lastAuth() != "Bearer "+educaKey {
		t.Fatalf("educa authorization = %q", educa.lastAuth())
	}
	post(ada, synth, map[string]any{"text": "Langsam.", "speed": 0.2}, http.StatusOK)
	if educa.lastSpeech()["speed"] != 0.7 {
		t.Fatalf("slow speed not clamped: %v", educa.lastSpeech())
	}
	post(ada, synth, map[string]any{"text": "Normal."}, http.StatusOK)
	if _, has := educa.lastSpeech()["speed"]; has {
		t.Fatalf("speed 0 sends no speed: %v", educa.lastSpeech())
	}

	// The settings' test: one sentence, ok, and no audio in the answer.
	tested := expect(admin, http.MethodPost, test, nil, http.StatusOK)
	if tested["ok"] != true || tested["bytes"] != float64(len(fakeWAV)) {
		t.Fatalf("test with educa: %v", tested)
	}
	const testSpoken = "This is a test of the voice provider, ticket four hundred eighty-one, " +
		"one thousand two hundred fifty euros and fifty cents, on September thirtieth twenty twenty-six at ten thirty."
	if got := educa.lastSpeech(); got["input"] != testSpoken || got["response_format"] != "wav" || tested["text"] != testSpoken {
		t.Fatalf("test request: %v, answer %v", got, tested)
	}

	// The provider gets words (#501): the reply's numbers, codes and emojis
	// written out in the request's language, or the one its words suggest.
	post(ada, synth, map[string]any{"language": "de-DE",
		"text": "Doch, ist drin – DLES-273, MR !475 🎉, um 10:30 für 1.250 €."}, http.StatusOK)
	if got := educa.lastSpeech()["input"]; got != "Doch, ist drin, D L E S zweihundertdreiundsiebzig, "+
		"Merge Request vierhundertfünfundsiebzig, um zehn Uhr dreißig für eintausendzweihundertfünfzig Euro." {
		t.Fatalf("normalised German: %q", got)
	}
	post(ada, synth, map[string]any{"text": "The export is at 12% and costs $5 👍", "stream": true}, http.StatusOK)
	if got := educa.lastSpeech()["input"]; got != "The export is at twelve percent and costs five dollars" {
		t.Fatalf("normalised English: %q", got)
	}
	post(ada, synth, map[string]any{"text": "🎉🎉 👍"}, http.StatusBadRequest)

	// Refusals before the server is asked.
	post(ada, synth, map[string]any{"text": "   "}, http.StatusBadRequest)
	post(ada, synth, map[string]any{"text": strings.Repeat("ä", 1001)}, http.StatusBadRequest)
	post(ada, synth, map[string]any{"text": "x", "instructions": strings.Repeat("i", 301)}, http.StatusBadRequest)
	post(ada, synth, map[string]any{"text": "x", "speed": -1}, http.StatusBadRequest)
	post(ada, synth, map[string]any{"text": "x", "voice": strings.Repeat("v", 101)}, http.StatusBadRequest)

	// The organisation's own server and key override educa AI.
	expect(ada, http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{"base_url": own.URL}, http.StatusForbidden)
	expect(admin, http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{"base_url": "ftp://x"}, http.StatusBadRequest)
	view = expect(admin, http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{
		"base_url": own.URL + "/v1/", "model": "kokoro", "voice": "af_bella", "key": ownKey,
	}, http.StatusOK)
	if view["base_url"] != own.URL || view["key_set"] != true ||
		view["effective"].(map[string]any)["source"] != "own" || view["transcribe"] != false {
		t.Fatalf("own server: %v", view)
	}
	post(ada, synth, map[string]any{"text": "Hallo."}, http.StatusOK)
	if got := own.lastSpeech(); got["model"] != "kokoro" || got["voice"] != "af_bella" || own.lastAuth() != "Bearer "+ownKey {
		t.Fatalf("request to the own server: %v, %q", got, own.lastAuth())
	}
	post(ada, synth, map[string]any{"text": "Hallo.", "model": "tts-1", "voice": "alloy"}, http.StatusOK)
	if got := own.lastSpeech(); got["model"] != "kokoro" || got["voice"] != "alloy" {
		t.Fatalf("a named voice, the organisation's model: %v", got)
	}

	// Streamed: MP3 passed through chunk by chunk — the first arrives
	// while the server still holds back the rest.
	gate := make(chan struct{})
	own.mu.Lock()
	own.gate = gate
	own.mu.Unlock()
	sr := ada.do(http.MethodPost, synth, map[string]any{"text": "Ein längerer Satz.", "stream": true})
	if sr.StatusCode != http.StatusOK || sr.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("stream: HTTP %d, %q", sr.StatusCode, sr.Header.Get("Content-Type"))
	}
	first := make([]byte, len("chunk-one|"))
	arrived := make(chan error, 1)
	go func() { _, err := io.ReadFull(sr.Body, first); arrived <- err }()
	select {
	case err := <-arrived:
		if err != nil || string(first) != "chunk-one|" {
			t.Fatalf("first chunk: %q %v", first, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the first chunk did not arrive before the rest was sent: the stream is buffered")
	}
	close(gate)
	rest, _ := io.ReadAll(sr.Body)
	sr.Body.Close()
	if string(rest) != "chunk-two|chunk-three" {
		t.Fatalf("the rest of the stream: %q", rest)
	}
	if got := own.lastSpeech(); got["response_format"] != "mp3" || got["stream_format"] != "audio" {
		t.Fatalf("streamed request: %v", got)
	}
	own.mu.Lock()
	own.gate = nil
	own.mu.Unlock()

	// A failing server: a short 502, before any audio.
	own.setFail(true)
	post(ada, synth, map[string]any{"text": "Hallo."}, http.StatusBadGateway)
	post(ada, synth, map[string]any{"text": "Hallo.", "stream": true}, http.StatusBadGateway)
	if m := expect(admin, http.MethodPost, test, nil, http.StatusOK); m["ok"] != false || m["error"] != "the voice provider answered HTTP 500" {
		t.Fatalf("test of a failing provider: %v", m)
	}
	own.setFail(false)

	// Recognition: off until an admin turns it on.
	turn := wavOf(32000)
	rawPost := func(path string, body []byte, want int) []byte {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, ada.base+path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "audio/wav")
		resp, err := ada.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		keep(raw)
		if resp.StatusCode != want {
			t.Fatalf("POST %s: HTTP %d, want %d: %s", path, resp.StatusCode, want, raw)
		}
		return raw
	}
	rawPost("/api/v1/speech/transcribe", turn, http.StatusConflict)
	view = expect(admin, http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{
		"base_url": own.URL, "model": "kokoro", "voice": "af_bella", "transcribe": true, "transcribe_model": "whisper-large-v3",
	}, http.StatusOK)
	if view["transcribe"] != true || view["key_set"] != true {
		t.Fatalf("recognition on, key kept: %v", view)
	}
	if m := expect(ada, http.MethodGet, "/api/v1/speech/model", nil, http.StatusOK); m["transcribe"] != true {
		t.Fatalf("transcribe flag: %v", m)
	}
	out := rawPost("/api/v1/speech/transcribe?language=de-DE", turn, http.StatusOK)
	if string(out) != "{\"text\":\"wie weit ist der Export\"}\n" {
		t.Fatalf("transcription: %s", out)
	}
	own.mu.Lock()
	form, size := own.forms[0], own.fileSizes[0]
	own.mu.Unlock()
	if form["model"] != "whisper-large-v3" || form["language"] != "de" || form["filename"] != "turn.wav" || size != len(turn) {
		t.Fatalf("transcription request: %v, %d bytes", form, size)
	}
	if own.lastAuth() != "Bearer "+ownKey {
		t.Fatalf("transcription authorization = %q", own.lastAuth())
	}
	rawPost("/api/v1/speech/transcribe", []byte("not a wav at all, just some bytes here to pass the length check"), http.StatusBadRequest)
	rawPost("/api/v1/speech/transcribe", wavOf(2<<20), http.StatusRequestEntityTooLarge)

	// 30 a minute per seat for synthesis.
	for i := 0; ; i++ {
		resp := ada.do(http.MethodPost, synth, map[string]any{"text": "Noch einmal."})
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		keep(raw)
		if resp.StatusCode == http.StatusTooManyRequests {
			break
		}
		if resp.StatusCode != http.StatusOK || i > 30 {
			t.Fatalf("synthesis %d: HTTP %d %s", i, resp.StatusCode, raw)
		}
	}
	// Another seat has its own budget.
	post(admin, synth, map[string]any{"text": "Hallo."}, http.StatusOK)

	for _, b := range bodies {
		if strings.Contains(b, ownKey) || strings.Contains(b, educaKey) {
			t.Fatalf("a key reached an answer: %s", b)
		}
	}

	// Removing the own server removes its key; educa AI applies again.
	view = expect(admin, http.MethodPatch, "/api/v1/org/voice-provider", map[string]any{"base_url": ""}, http.StatusOK)
	if view["key_set"] != false || view["effective"].(map[string]any)["source"] != "educa" {
		t.Fatalf("cleared: %v", view)
	}
}
