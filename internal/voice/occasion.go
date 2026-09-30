package voice

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

/* Voices per occasion, chosen by who is spoken to (#471).
 *
 * One voice cannot fit both a customer mail and a quick answer to a colleague,
 * and the person spoken to matters as much as the occasion: sales wants what
 * it means for the customer, engineering the ticket and the branch. So a voice
 * is assigned per occasion at three levels, and the one in effect is chosen at
 * the moment of writing — the first that applies:
 *
 *   1. the addressed person's department × occasion,
 *   2. the agent × occasion,
 *   3. the organisation × occasion.
 *
 * The department's "how to speak with us" line is added whichever voice
 * applies. In a group with people of several departments, level 1 is skipped —
 * no one department's voice is right for all of them — and the lines of every
 * department involved are added, bounded. Customers have no department:
 * levels 2 and 3.
 *
 * This file is the rule without the database, so it can be tested as a rule. */

// Occasion is what a text is written for.
type Occasion string

const (
	// OccasionChat is the team chat: the triage, the narration, and the runs of
	// tasks that came from a conversation.
	OccasionChat Occasion = "chat"
	// OccasionCustomers is outward: mails, tickets, replies in target systems.
	OccasionCustomers Occasion = "customers"
	// OccasionPublications is blog, docs, offers.
	OccasionPublications Occasion = "publications"
)

// Occasions in the order the interface shows them.
var Occasions = []Occasion{OccasionChat, OccasionCustomers, OccasionPublications}

// ParseOccasion refuses what is not one of the three.
func ParseOccasion(s string) (Occasion, error) {
	o := Occasion(strings.ToLower(strings.TrimSpace(s)))
	for _, known := range Occasions {
		if o == known {
			return o, nil
		}
	}
	return "", fmt.Errorf("%w: an occasion is %q, %q or %q", ErrInvalid, OccasionChat, OccasionCustomers, OccasionPublications)
}

// Slots are the voices one holder (an agent, a department, the organisation)
// names per occasion. A missing key is an empty slot.
type Slots map[Occasion]uuid.UUID

// Level says which of the three levels chose a voice.
type Level string

const (
	LevelDepartment Level = "department"
	LevelAgent      Level = "agent"
	LevelOrg        Level = "org"
	// LevelNone: no level names a voice for the occasion.
	LevelNone Level = "none"
)

// AudienceNoteMax: a line, not a style guide — it goes into every turn that
// speaks to somebody of that department.
const AudienceNoteMax = 400

// audienceNotesMax bounds a group: the lines of at most this many departments,
// and at most audienceNotesChars characters of them together.
const (
	audienceNotesMax   = 4
	audienceNotesChars = 1200
)

// AudienceDepartment is one department among the people spoken to.
type AudienceDepartment struct {
	ID    uuid.UUID
	Name  string
	Note  string
	Slots Slots
}

// Audience is who a text is written to, as far as departments tell.
//
// Addressed is the department of the person the text answers — the one who
// wrote the message, or asked for the task. Nil for a customer, and for a
// person without a department. Others are the departments of the other
// people involved: in a group, those who wrote in the last few messages. A
// department may turn up in both, and more than once; the rule counts it once.
type Audience struct {
	Addressed *AudienceDepartment
	Others    []AudienceDepartment
}

// AudienceNote is one department's line, as it is added to a prompt.
type AudienceNote struct {
	Department string `json:"department"`
	Note       string `json:"note"`
}

// Choice is the voice in effect and why.
type Choice struct {
	VoiceID  uuid.UUID `json:"voice_id"`
	Occasion Occasion  `json:"occasion"`
	Level    Level     `json:"level"`
	// Department is the department whose slot chose the voice (level 1).
	Department string `json:"department,omitempty"`
	// Notes are the "how to speak with us" lines that go with it.
	Notes []AudienceNote `json:"notes,omitempty"`
}

// Found reports whether a voice was chosen.
func (c Choice) Found() bool { return c.VoiceID != uuid.Nil }

// Reason is why, in the short form the recording, the chat message's meta
// and the "who gets what" table carry: "department:Sales×chat",
// "agent×customers", "org×chat", and "none×chat" when no level names one.
func (c Choice) Reason() string {
	switch c.Level {
	case LevelDepartment:
		return fmt.Sprintf("department:%s×%s", c.Department, c.Occasion)
	case LevelAgent, LevelOrg:
		return fmt.Sprintf("%s×%s", c.Level, c.Occasion)
	}
	return fmt.Sprintf("%s×%s", LevelNone, c.Occasion)
}

// Choose applies the rule: department, then agent, then organisation.
func Choose(occ Occasion, aud Audience, agent, org Slots) Choice {
	c := Choice{Occasion: occ, Level: LevelNone, Notes: aud.notes()}
	// Level 1 only when the addressed person's department is the only one
	// involved: in a group with several, no one department's voice is right
	// for all of them, and the agent's own applies with everybody's lines.
	if d := aud.Addressed; d != nil && aud.single() {
		if id, ok := d.Slots[occ]; ok && id != uuid.Nil {
			c.VoiceID, c.Level, c.Department = id, LevelDepartment, d.Name
			return c
		}
	}
	if id, ok := agent[occ]; ok && id != uuid.Nil {
		c.VoiceID, c.Level = id, LevelAgent
		return c
	}
	if id, ok := org[occ]; ok && id != uuid.Nil {
		c.VoiceID, c.Level = id, LevelOrg
	}
	return c
}

// single reports whether nobody of another department than the addressed
// person's is involved.
func (a Audience) single() bool {
	for _, d := range a.Others {
		if d.ID != a.Addressed.ID {
			return false
		}
	}
	return true
}

// notes are the lines of the departments involved — the addressed person's
// always and first, then the others', each department once — bounded: at
// most audienceNotesMax lines and audienceNotesChars characters together.
func (a Audience) notes() []AudienceNote {
	all := a.Others
	if a.Addressed != nil {
		all = append([]AudienceDepartment{*a.Addressed}, a.Others...)
	}
	var out []AudienceNote
	seen := map[uuid.UUID]bool{}
	total := 0
	for _, d := range all {
		if seen[d.ID] {
			continue
		}
		seen[d.ID] = true
		note := strings.Join(strings.Fields(d.Note), " ")
		if note == "" {
			continue
		}
		note = cut(note, AudienceNoteMax)
		n := len([]rune(note))
		if len(out) == audienceNotesMax || total+n > audienceNotesChars {
			break
		}
		total += n
		out = append(out, AudienceNote{Department: d.Name, Note: note})
	}
	return out
}

// AudiencePrompt is the notes as the turns and the runs read them. Empty
// when no department said anything.
func AudiencePrompt(notes []AudienceNote) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	if len(notes) == 1 {
		fmt.Fprintf(&b, "How the %s department wants to be spoken to (set by your organisation): %s", notes[0].Department, notes[0].Note)
		return b.String()
	}
	b.WriteString("Several departments read along. How each wants to be spoken to (set by your organisation) — write so that it works for all of them:")
	for _, n := range notes {
		fmt.Fprintf(&b, "\n- %s: %s", n.Department, n.Note)
	}
	return b.String()
}

// Bounds of the chat block: the chat is two sentences, and the block stands
// in every turn — the card is cut and at most two passages come along.
const (
	chatCardMax      = 1200
	chatExemplars    = 2
	chatExemplarChar = 300
)

// ChatPrompt is a voice as the two cheap turns of the team chat read it:
// the released card, compact, and two passages. Only what acts goes in — the
// released card and the exemplars an agent reads, never a draft.
func ChatPrompt(v Voice) string {
	card := strings.TrimSpace(v.ReleasedCard)
	if card == "" && len(v.Exemplars) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "How you write — the voice %q, which your organisation chose for this conversation. Keep its hand; a chat line stays a chat line, shorter than anything below.", v.Name)
	if card != "" {
		b.WriteString("\n")
		b.WriteString(cut(card, chatCardMax))
	}
	n := 0
	for _, ex := range v.Exemplars {
		if n == chatExemplars {
			break
		}
		t := strings.Join(strings.Fields(ex.Text), " ")
		if t == "" {
			continue
		}
		if n == 0 {
			b.WriteString("\nPassages in this voice (the movement, not the topic):")
		}
		fmt.Fprintf(&b, "\n> %s", cut(t, chatExemplarChar))
		n++
	}
	return b.String()
}

// occasionLine is the explicit hint a task can carry: a line "occasion:
// publications" in its body (a HEARTBEAT aufgabe: can say it too).
var occasionLine = regexp.MustCompile(`(?im)^\s*occasion:\s*(chat|customers|publications)\s*$`)

// TaskOccasion is the occasion of a run. A task from a conversation writes
// for the chat: its result is read out there. Otherwise a line "occasion:
// <x>" in the task says it; without one it is customers — what a run writes
// into a target system is outward. A publication has to say so: the platform
// cannot tell a blog post from a ticket reply by looking at the task.
func TaskOccasion(fromConversation bool, title, body string) Occasion {
	if fromConversation {
		return OccasionChat
	}
	if m := occasionLine.FindStringSubmatch(title + "\n" + body); m != nil {
		return Occasion(strings.ToLower(m[1]))
	}
	return OccasionCustomers
}

// OutwardOccasion is the occasion of a text leaving through a target system
// during a run: the task's, unless the task is a chat one — a mail sent from a
// chat task still goes to a customer.
func OutwardOccasion(task Occasion) Occasion {
	if task == OccasionChat {
		return OccasionCustomers
	}
	return task
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + " […]"
}
