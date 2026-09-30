package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
)

// educaVoices is the shape educa AI answers GET /api/tts/voices with.
const educaVoices = `{"default":{"language":"de","name":"thorsten"},"fetched_at":"2026-09-30T10:00:00Z","voices":[
 {"display_name":"Bernd","language":"de","name":"bernd"},
 {"display_name":"Erika","language":"de","name":"erika"},
 {"display_name":"Thorsten","language":"de","name":"thorsten"},
 {"display_name":"Carl","language":"en","name":"carl"},
 {"display_name":"Linda","language":"en-US","name":"linda"},
 {"display_name":"","language":"fr","name":"gabriel"},
 {"display_name":"Doppelt","language":"de","name":"bernd"},
 {"display_name":"Ohne Namen","language":"de","name":"  "}]}`

func listFrom(t *testing.T, status int, body string) (providerVoiceList, string) {
	t.Helper()
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/tts/voices" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	l, err := voiceProvider{base: srv.URL, key: "k-1"}.voices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return l, auth
}

// The list is read with the synthesis credentials, cleaned — duplicates
// and nameless entries dropped, languages cut to their base, a missing
// display name filled with the name — and sorted by language and name.
func TestProviderVoiceList(t *testing.T) {
	l, auth := listFrom(t, http.StatusOK, educaVoices)
	if auth != "Bearer k-1" {
		t.Fatalf("authorization = %q", auth)
	}
	if !l.Listed || l.Default == nil || l.Default.Name != "thorsten" || l.Default.DisplayName != "Thorsten" || l.Default.Language != "de" {
		t.Fatalf("list: %+v, default %+v", l, l.Default)
	}
	want := []providerVoice{
		{"bernd", "Bernd", "de"}, {"erika", "Erika", "de"}, {"thorsten", "Thorsten", "de"},
		{"carl", "Carl", "en"}, {"linda", "Linda", "en"}, {"gabriel", "gabriel", "fr"},
	}
	if len(l.Voices) != len(want) {
		t.Fatalf("voices: %+v", l.Voices)
	}
	for i := range want {
		if l.Voices[i] != want[i] {
			t.Fatalf("voice %d = %+v, want %+v", i, l.Voices[i], want[i])
		}
	}
}

// A provider without the list — 404, another status, another shape — has
// none: listed is false, not an error.
func TestProviderWithoutVoiceList(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
	}{{http.StatusNotFound, "not found"}, {http.StatusUnauthorized, "{}"}, {http.StatusOK, "<html>"}, {http.StatusOK, `{"data":[]}`}} {
		l, _ := listFrom(t, c.status, c.body)
		if l.Listed || len(l.Voices) != 0 || l.Default != nil {
			t.Errorf("%d %q: %+v", c.status, c.body, l)
		}
	}
}

// The assignment: deterministic, spread over the organisation's agents in
// the order they were hired within the call's language, and the defaults
// only where the language has no voice.
func TestAssignSpokenVoice(t *testing.T) {
	l, _ := listFrom(t, http.StatusOK, educaVoices)
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	for i, want := range []string{"bernd", "erika", "thorsten", "bernd"} {
		v := assignSpokenVoice(l, "de-DE", ids[i], ids, "")
		if v.Source != spokenAssigned || v.Language != "de" || v.Name != want {
			t.Fatalf("agent %d: %+v, want %s", i, v, want)
		}
		if again := assignSpokenVoice(l, "de", ids[i], ids, ""); again.Name != v.Name {
			t.Fatalf("not deterministic: %s then %s", v.Name, again.Name)
		}
	}
	// A new hire comes last and moves nobody.
	later := append(slices.Clone(ids), uuid.New())
	for i := range ids {
		if a, b := assignSpokenVoice(l, "de", ids[i], ids, ""), assignSpokenVoice(l, "de", ids[i], later, ""); a.Name != b.Name {
			t.Fatalf("agent %d moved from %s to %s", i, a.Name, b.Name)
		}
	}
	// A draft takes the place after the hired agents.
	if v := assignSpokenVoice(l, "de", uuid.New(), ids, ""); v.Name != "erika" {
		t.Fatalf("draft: %+v", v)
	}
	if v := assignSpokenVoice(l, "en", ids[0], ids, ""); v.Name != "carl" || v.DisplayName != "Carl" {
		t.Fatalf("English: %+v", v)
	}
	// No language given: the provider default's.
	if v := assignSpokenVoice(l, "", ids[2], ids, ""); v.Language != "de" || v.Source != spokenAssigned {
		t.Fatalf("without a language: %+v", v)
	}
	// A language without voices: the configured default, else the list's.
	if v := assignSpokenVoice(l, "it", ids[0], ids, "erika"); v.Source != spokenProviderDefault || v.Name != "erika" || v.DisplayName != "Erika" {
		t.Fatalf("configured default: %+v", v)
	}
	if v := assignSpokenVoice(l, "it", ids[0], ids, ""); v.Source != spokenProviderDefault || v.Name != "thorsten" {
		t.Fatalf("the list's default: %+v", v)
	}
	// No list at all: the configured default, or nothing.
	none := providerVoiceList{}
	if v := assignSpokenVoice(none, "de", ids[0], ids, "af_bella"); v.Name != "af_bella" || v.Source != spokenProviderDefault {
		t.Fatalf("without a list: %+v", v)
	}
	if v := assignSpokenVoice(none, "de", ids[0], ids, ""); v.Name != "" || v.Source != spokenProviderDefault {
		t.Fatalf("nothing: %+v", v)
	}
}

// The cache keeps a list per organisation and provider, and forgets it
// when the settings change.
func TestVoiceListCache(t *testing.T) {
	var c voiceListCache
	org := uuid.New()
	l := providerVoiceList{Listed: true}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	c.put(org, voiceListEntry{fingerprint: "a", list: l, until: now.Add(voiceListTTL)})
	if _, ok := c.get(org, "a", now); !ok {
		t.Fatal("fresh list not kept")
	}
	if _, ok := c.get(org, "b", now); ok {
		t.Fatal("another provider's list returned")
	}
	if _, ok := c.get(org, "a", now.Add(voiceListTTL+1)); ok {
		t.Fatal("stale list returned")
	}
	c.forget(org)
	if _, ok := c.get(org, "a", now); ok {
		t.Fatal("forgotten list returned")
	}
}

// The order voices are spread in: hired agents by their hiring, drafts
// left out.
func TestHiredAgentIDs(t *testing.T) {
	day := func(d int) *time.Time { v := time.Date(2026, 9, d, 0, 0, 0, 0, time.UTC); return &v }
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	got := hiredAgentIDs([]agents.Agent{{ID: a, HiredAt: day(3)}, {ID: b}, {ID: c, HiredAt: day(1)}})
	if len(got) != 2 || got[0] != c || got[1] != a {
		t.Fatalf("order: %v, want %v %v", got, c, a)
	}
}
