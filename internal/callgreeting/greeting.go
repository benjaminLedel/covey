// Package callgreeting writes the sentence an agent opens a call with
// (#513): one fast control-plane turn over a little context — the time of
// day, what the agent works on, what the person and the agent last spoke
// about — so that the greeting fits the situation instead of repeating a
// template. The app keeps its templates (#506) as the fallback: whatever
// goes wrong here, the call greets anyway.
//
// Only what the person could already see goes in: the conversation's own
// last messages and the titles of the agent's tasks, which the backlog
// shows every member of the organisation.
package callgreeting

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"covey/internal/llm"
	"covey/internal/speech/normalise"
)

// MaxRunes is the longest greeting the guard lets through: two short
// spoken sentences.
const MaxRunes = 180

// Timeout bounds the model's turn. The app waits a little longer and then
// greets from its templates.
const Timeout = 3 * time.Second

// ErrRejected: the model's answer is not a greeting the call may say.
var ErrRejected = errors.New("the answer is not a greeting")

// Line is one message of the conversation as the greeting sees it.
type Line struct {
	// Agent: the agent wrote it; otherwise Author is the person's name.
	Agent  bool
	Author string
	Text   string
	At     time.Time
}

// Context is everything the greeting is written from. Empty fields are
// left out of the prompt.
type Context struct {
	AgentName  string
	Department string
	// FirstName is the person's first name, FullName their display name.
	FirstName string
	FullName  string
	// Language is the conversation's BCP 47 tag; empty lets the recent
	// messages decide.
	Language string
	// Local is the person's clock, with their offset; Weekday overrides the
	// day Local names when the app says it.
	Local   time.Time
	Weekday string
	// Address is du, sie or empty; Tone and ToneNote the chat tone in
	// effect (voice.ChatTone).
	Address  string
	Tone     string
	ToneNote string
	// InProgress are the titles of the agent's tasks in progress, at most
	// three; DoneToday the title of the one it finished last today.
	InProgress []string
	DoneToday  string
	// Recent are the conversation's last messages, oldest first.
	Recent []Line
}

// system is the whole instruction. The examples set the length and the
// register; they are examples of a style, not templates to fill.
const system = `You write the first thing an AI agent says when a person calls it on the phone. It is spoken aloud by a voice, right after the call connects.

- One or two short sentences, at most 180 characters, ending in one open question to the caller.
- Greet by the time of day and use the person's first name when the address is informal; with a formal address ("Sie", "usted", "vous") use no name.
- Make it fit the situation, lightly: what you are working on right now, what you finished today, or what the two of you spoke about recently ("this morning", "yesterday"). Pick at most one of these, and only when it is natural; a plain friendly greeting is fine too. Never invent anything that is not in the context.
- If there are no earlier messages, say your name.
- Spoken language only: no ticket numbers, ids, codes, links, e-mail addresses, emoji, lists or markdown. Say what a task is about in plain words, never its key.
- Write in the language given; if none is given, in the language of the recent messages; if there are none, in English.
- Follow the address (du/Sie) and the tone exactly.

Examples of the style (not templates):
- Guten Morgen, Ada! Ich sitze gerade an der Übersicht für die Kundenumfrage. Was kann ich für dich tun?
- Guten Tag! Heute Vormittag ging es um die Rechnung für März. Möchten Sie daran anknüpfen?
- Hallo Ada, der Newsletter ist heute raus. Was steht als Nächstes an?
- Morning, Ada, hope you had a good weekend. What can I do for you?
- Hi Ada, it's Paula. Shall we pick up the release notes from yesterday, or is it something new?

Output only the greeting, without quotes or a preamble.`

// Prompt is the instruction and the context as the model reads them.
func Prompt(c Context) (string, string) {
	var b strings.Builder
	line := func(label, v string) {
		if v = clip(v, 200); v != "" {
			b.WriteString(label + ": " + v + "\n")
		}
	}
	agent := clip(c.AgentName, 80)
	if d := clip(c.Department, 80); d != "" && agent != "" {
		agent += " (department: " + d + ")"
	}
	line("You are", agent)
	line("The caller", c.FirstName)
	if c.FullName != "" && c.FullName != c.FirstName {
		line("The caller's full name", c.FullName)
	}
	switch strings.ToLower(strings.TrimSpace(c.Address)) {
	case "du":
		line("Address", `informal (German "du")`)
	case "sie":
		line("Address", `formal (German "Sie")`)
	}
	switch c.Tone {
	case "casual":
		line("Tone", "casual, like with a colleague you know well")
	case "matter_of_fact":
		line("Tone", "friendly and matter-of-fact")
	case "formal":
		line("Tone", "polite and formal")
	}
	line("Tone note", c.ToneNote)
	line("Language", c.Language)
	if !c.Local.IsZero() {
		day := strings.TrimSpace(c.Weekday)
		if day == "" {
			day = c.Local.Weekday().String()
		}
		line("The caller's local time", fmt.Sprintf("%s, %s (%s)", day, c.Local.Format("15:04"), partOfDay(c.Local)))
	}
	if len(c.InProgress) > 0 {
		b.WriteString("You are working on now:\n")
		for i, t := range c.InProgress {
			if i == 3 {
				break
			}
			if t = clip(t, 120); t != "" {
				b.WriteString("- " + t + "\n")
			}
		}
	}
	line("You finished today", clip(c.DoneToday, 120))
	if len(c.Recent) == 0 {
		b.WriteString("There are no earlier messages between you and the caller.\n")
	} else {
		b.WriteString("The last messages between you and the caller (oldest first):\n")
		for _, m := range c.Recent {
			who := clip(m.Author, 80)
			if m.Agent {
				who = "you"
			} else if who == "" {
				who = "the caller"
			}
			when := ""
			if !c.Local.IsZero() && !m.At.IsZero() {
				when = relative(m.At, c.Local) + ", "
			}
			b.WriteString("- " + when + who + ": " + clip(m.Text, 200) + "\n")
		}
	}
	return system, b.String()
}

// Write asks the model for the greeting and returns it guarded and
// normalised for speech.
func Write(ctx context.Context, p llm.Provider, c Context) (string, error) {
	sys, user := Prompt(c)
	out, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierFast,
		MaxTokens:  160,
		NoThinking: true,
		System:     sys,
		Messages:   []llm.Message{{Role: "user", Content: user}},
	})
	if err != nil {
		return "", err
	}
	g, err := Guard(out)
	if err != nil {
		return "", err
	}
	if g = strings.TrimSpace(normalise.Normalise(g, c.Language)); g == "" {
		return "", ErrRejected
	}
	return g, nil
}

var (
	reLink = regexp.MustCompile(`(?i)https?://\S+|www\.\S+|\S+@\S+\.\S+`)
	reID   = regexp.MustCompile(`(?i:\b[0-9a-f]{8}-[0-9a-f]{4}-)|#\d|![0-9]|\b[A-Z]{2,}[0-9]*-\d+\b|(?i:\b[0-9a-f]*[0-9][0-9a-f]*[a-f][0-9a-f]{5,}\b)`)
	reMark = regexp.MustCompile("[*_`#>|\\[\\]]")
)

// Guard makes the model's answer a greeting or refuses it: emoji and stray
// markup go, the answer is cut after its first question, and it has to
// end in that question within two sentences and MaxRunes. Links and ids
// refuse it: they would have to be read out.
func Guard(s string) (string, error) {
	s = strings.TrimSpace(s)
	for _, label := range []string{"Greeting:", "Begrüßung:"} {
		s = strings.TrimSpace(strings.TrimPrefix(s, label))
	}
	s = dropPictographs(s)
	s = reMark.ReplaceAllString(s, "")
	s = strings.Trim(strings.TrimSpace(s), "\"'„“”«»")
	s = strings.Join(strings.Fields(s), " ")
	s = strings.ReplaceAll(s, " ,", ",")
	if s == "" {
		return "", ErrRejected
	}
	if reLink.MatchString(s) || reID.MatchString(s) {
		return "", fmt.Errorf("%w: it carries a link or an id", ErrRejected)
	}
	parts := sentences(s)
	q := -1
	for i, p := range parts {
		if strings.HasSuffix(p, "?") || strings.HasSuffix(p, "？") {
			q = i
			break
		}
	}
	if q < 0 || q > 1 {
		return "", fmt.Errorf("%w: no question within two sentences", ErrRejected)
	}
	sep := " "
	if cjk(s) {
		sep = ""
	}
	out := strings.Join(parts[:q+1], sep)
	if utf8.RuneCountInString(out) > MaxRunes {
		return "", fmt.Errorf("%w: longer than %d characters", ErrRejected, MaxRunes)
	}
	return out, nil
}

// sentences splits after . ! ? … and their full-width forms.
func sentences(s string) []string {
	var out []string
	rs := []rune(s)
	start := 0
	for i, r := range rs {
		end := false
		switch r {
		case '。', '！', '？':
			end = true
		case '.', '!', '?', '…':
			end = i+1 == len(rs) || unicode.IsSpace(rs[i+1])
		}
		if end {
			if p := strings.TrimSpace(string(rs[start : i+1])); p != "" {
				out = append(out, p)
			}
			start = i + 1
		}
	}
	if p := strings.TrimSpace(string(rs[start:])); p != "" {
		out = append(out, p)
	}
	return out
}

func cjk(s string) bool {
	for _, r := range s {
		if unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana) {
			return true
		}
	}
	return false
}

// dropPictographs takes out emoji and what joins them.
func dropPictographs(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 0x1F000 && r <= 0x1FAFF, r >= 0x2600 && r <= 0x27BF, r >= 0xFE00 && r <= 0xFE0F, r == 0x200D, r == 0x20E3:
			return -1
		}
		return r
	}, s)
}

// partOfDay names the part of the day a greeting fits.
func partOfDay(t time.Time) string {
	switch h := t.Hour(); {
	case h >= 5 && h < 12:
		return "morning"
	case h >= 12 && h < 18:
		return "afternoon"
	case h >= 18 && h < 23:
		return "evening"
	}
	return "night"
}

// relative says when a message was written, on the caller's clock:
// "today 08:40, morning", "yesterday 17:02", "Monday 10:15", or the date.
func relative(at, now time.Time) string {
	at = at.In(now.Location())
	y1, m1, d1 := at.Date()
	day := time.Date(y1, m1, d1, 0, 0, 0, 0, now.Location())
	y2, m2, d2 := now.Date()
	today := time.Date(y2, m2, d2, 0, 0, 0, 0, now.Location())
	clock := at.Format("15:04")
	switch days := int(today.Sub(day).Hours() / 24); {
	case days <= 0:
		return "today " + clock + " (" + partOfDay(at) + ")"
	case days == 1:
		return "yesterday " + clock
	case days < 7:
		return at.Weekday().String() + " " + clock
	}
	return at.Format("2006-01-02")
}

// clip flattens a text to one line and cuts it at n runes; links go, they
// are nothing a greeting says.
func clip(s string, n int) string {
	s = reLink.ReplaceAllString(s, "")
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		s = strings.TrimSpace(string([]rune(s)[:n])) + "…"
	}
	return s
}

// FirstName is the first word of a display name — "Ada" of "Ada Berger",
// the whole of an e-mail-like name before its @.
func FirstName(display string) string {
	display = strings.TrimSpace(display)
	if i := strings.IndexByte(display, '@'); i > 0 {
		display = display[:i]
	}
	if f := strings.Fields(display); len(f) > 0 {
		return f[0]
	}
	return ""
}
