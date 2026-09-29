// Package push tells people's devices that an agent needs them or said
// something (#379): a question, an answer, a result, an error.
//
// Apple delivers to an app only for whoever holds the app's APNs key, and a
// third party running covey with the app from the store cannot hold it;
// Google the same for the app's Firebase project (#424). So there are
// senders behind one port: APNs and FCM, when this instance has the key, and
// Relay, which hands the notification to an instance that has — by default
// the public one. Senders picks between them by the device's platform. Any
// covey binary with a key can be that relay (Handler).
//
// What travels is small on purpose: who, what, a badge, and the agent to
// open. The first line of what was said only when the organisation turned
// the preview on.
package push

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Message is one notification for one device.
type Message struct {
	Token string `json:"token"`
	// Platform: ios, macos or android; empty is ios, as an instance from
	// before Android relays it.
	Platform    string `json:"platform,omitempty"`
	Environment string `json:"environment"` // production | development; android is production
	Title       string `json:"title"`
	Body        string `json:"body"`
	Badge       int    `json:"badge"`
	// AgentID is the thread a tap opens, and the thread notifications are
	// grouped under.
	AgentID string `json:"agent_id"`
	// Sound is a file in the app's bundle, "default", or empty for none
	// (#381).
	Sound string `json:"sound,omitempty"`
}

// Sounds are the families a device can choose, besides "system" and "none".
var Sounds = []string{"bot", "schar", "glas"}

// SoundFor is the file a notification of kind plays for a device that chose
// pref: covey-<family>-<kind>.caf from the app's bundle, the system's
// sound, or none.
func SoundFor(pref, kind string) string {
	switch pref {
	case "system":
		return "default"
	case "none":
		return ""
	}
	for _, f := range Sounds {
		if f == pref {
			return "covey-" + f + "-" + kind + ".caf"
		}
	}
	return "covey-bot-" + kind + ".caf"
}

var soundName = regexp.MustCompile(`^(default|covey-(bot|schar|glas)-(question|answer|result|error)\.caf)?$`)

// Limits a relay enforces, and a sender keeps to.
const (
	MaxTitle = 120
	MaxBody  = 400
	// MaxToken: an APNs token is 64 hex characters; Google gives no bound
	// for an FCM token, which is about 160 today.
	MaxToken = 512
)

// Sender delivers one notification.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Senders sends an Android device's notification through FCM and every
// other through APNs. Where the instance holds no key for the platform it
// hands the notification to Fallback, the relay; without one it fails.
type Senders struct {
	APNs, FCM Sender
	Fallback  Sender
}

func (s Senders) Send(ctx context.Context, m Message) error {
	direct, name := s.APNs, "APNs"
	if m.Platform == "android" {
		direct, name = s.FCM, "FCM"
	}
	switch {
	case direct != nil:
		return direct.Send(ctx, m)
	case s.Fallback != nil:
		return s.Fallback.Send(ctx, m)
	}
	return fmt.Errorf("push: no %s key and no relay", name)
}

// ErrGone: the device no longer takes notifications — the app was removed,
// or the token was replaced. Its registration goes.
var ErrGone = errors.New("device token no longer valid")

// Clip keeps a text within n characters.
func Clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Valid says whether a message fits what any sender accepts.
func (m Message) Valid() bool {
	return m.Token != "" && len(m.Token) <= MaxToken &&
		(m.Platform == "" || m.Platform == "ios" || m.Platform == "macos" || m.Platform == "android") &&
		(m.Environment == "production" || m.Environment == "development") &&
		m.Title != "" && len([]rune(m.Title)) <= MaxTitle && len([]rune(m.Body)) <= MaxBody &&
		m.Badge >= 0 && m.Badge < 100000 && len(m.AgentID) <= 64 && soundName.MatchString(m.Sound)
}

// FirstLine is the start of a text as a list shows it: one line, at most n
// characters, Markdown's emphasis and headings left out.
func FirstLine(text string, n int) string {
	text = strings.TrimSpace(text)
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#>*- "))
		if line == "" {
			continue
		}
		line = strings.NewReplacer("**", "", "__", "", "`", "").Replace(line)
		if r := []rune(line); len(r) > n {
			return strings.TrimSpace(string(r[:n-1])) + "…"
		}
		return line
	}
	return ""
}
