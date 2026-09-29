package voice

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestChooseTakesTheFirstLevelThatNamesAVoice(t *testing.T) {
	deptVoice, agentVoice, orgVoice := uuid.New(), uuid.New(), uuid.New()
	sales := AudienceDepartment{ID: uuid.New(), Name: "Sales", Note: "What it means for the customer.",
		Slots: Slots{OccasionChat: deptVoice}}
	agent := Slots{OccasionChat: agentVoice, OccasionCustomers: agentVoice}
	org := Slots{OccasionChat: orgVoice, OccasionPublications: orgVoice}

	cases := []struct {
		name   string
		occ    Occasion
		aud    Audience
		agent  Slots
		want   uuid.UUID
		reason string
	}{
		{"department wins", OccasionChat, Audience{Addressed: &sales}, agent, deptVoice, "department:Sales×chat"},
		{"a department without the occasion falls to the agent", OccasionCustomers, Audience{Addressed: &sales}, agent, agentVoice, "agent×customers"},
		{"the agent without the occasion falls to the org", OccasionPublications, Audience{Addressed: &sales}, agent, orgVoice, "org×publications"},
		{"no department: the agent", OccasionChat, Audience{}, agent, agentVoice, "agent×chat"},
		{"an empty agent: the org", OccasionChat, Audience{}, Slots{}, orgVoice, "org×chat"},
		{"a nil slot is empty", OccasionChat, Audience{}, Slots{OccasionChat: uuid.Nil}, orgVoice, "org×chat"},
		{"nothing anywhere", OccasionCustomers, Audience{}, Slots{}, uuid.Nil, "none×customers"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Choose(c.occ, c.aud, c.agent, org)
			if got.VoiceID != c.want {
				t.Errorf("voice %v, want %v", got.VoiceID, c.want)
			}
			if got.Reason() != c.reason {
				t.Errorf("reason %q, want %q", got.Reason(), c.reason)
			}
			if got.Found() != (c.want != uuid.Nil) {
				t.Errorf("Found() = %v", got.Found())
			}
		})
	}
}

// In a group with people of several departments no one department's voice
// fits all of them: level 2 applies, with everybody's lines.
func TestChooseInAMixedGroupSkipsTheDepartment(t *testing.T) {
	deptVoice, agentVoice := uuid.New(), uuid.New()
	sales := AudienceDepartment{ID: uuid.New(), Name: "Sales", Note: "The customer first.", Slots: Slots{OccasionChat: deptVoice}}
	eng := AudienceDepartment{ID: uuid.New(), Name: "Engineering", Note: "Ticket and branch."}

	mixed := Choose(OccasionChat, Audience{Addressed: &sales, Others: []AudienceDepartment{eng, sales}}, Slots{OccasionChat: agentVoice}, nil)
	if mixed.VoiceID != agentVoice || mixed.Reason() != "agent×chat" {
		t.Fatalf("a mixed group has to fall to the agent: %+v %s", mixed, mixed.Reason())
	}
	if len(mixed.Notes) != 2 || mixed.Notes[0].Department != "Sales" || mixed.Notes[1].Department != "Engineering" {
		t.Fatalf("the addressed department's line first, each once: %+v", mixed.Notes)
	}

	// The others all of the addressed person's department: still level 1.
	same := Choose(OccasionChat, Audience{Addressed: &sales, Others: []AudienceDepartment{sales, sales}}, Slots{OccasionChat: agentVoice}, nil)
	if same.VoiceID != deptVoice || len(same.Notes) != 1 {
		t.Fatalf("one department is not a mixed group: %+v", same)
	}

	// Somebody without a department asked, the others are of one: there is
	// no addressed department, so no level 1 — but the others' lines come.
	none := Choose(OccasionChat, Audience{Others: []AudienceDepartment{sales}}, Slots{OccasionChat: agentVoice}, nil)
	if none.VoiceID != agentVoice || len(none.Notes) != 1 || none.Notes[0].Department != "Sales" {
		t.Fatalf("no addressed department: %+v", none)
	}
}

// The addressed person's line comes whichever voice applies, also when the
// department names none.
func TestTheAddressedNoteComesWithEveryVoice(t *testing.T) {
	agentVoice := uuid.New()
	eng := AudienceDepartment{ID: uuid.New(), Name: "Engineering", Note: "  Name the   ticket\nand the branch. "}
	c := Choose(OccasionCustomers, Audience{Addressed: &eng}, Slots{OccasionCustomers: agentVoice}, nil)
	if c.Level != LevelAgent || len(c.Notes) != 1 || c.Notes[0].Note != "Name the ticket and the branch." {
		t.Fatalf("%+v", c)
	}
	if p := AudiencePrompt(c.Notes); !strings.Contains(p, "Engineering department") || !strings.Contains(p, "Name the ticket") {
		t.Fatalf("prompt: %q", p)
	}
	if AudiencePrompt(nil) != "" {
		t.Fatal("no notes, no block")
	}
}

func TestAudienceNotesAreBounded(t *testing.T) {
	var others []AudienceDepartment
	for i := 0; i < 8; i++ {
		others = append(others, AudienceDepartment{ID: uuid.New(), Name: "D" + string(rune('A'+i)), Note: strings.Repeat("x", 390)})
	}
	long := AudienceDepartment{ID: uuid.New(), Name: "Long", Note: strings.Repeat("y ", 600)}
	c := Choose(OccasionChat, Audience{Addressed: &long, Others: others}, nil, nil)
	if len(c.Notes) > audienceNotesMax {
		t.Fatalf("%d notes", len(c.Notes))
	}
	total := 0
	for _, n := range c.Notes {
		if r := len([]rune(n.Note)); r > AudienceNoteMax+len(" […]") {
			t.Fatalf("a note of %d characters", r)
		}
		total += len([]rune(n.Note))
	}
	if total > audienceNotesChars {
		t.Fatalf("%d characters of notes", total)
	}
	if c.Notes[0].Department != "Long" {
		t.Fatalf("the addressed line first: %+v", c.Notes[0])
	}
	p := AudiencePrompt(c.Notes)
	if !strings.Contains(p, "Several departments") {
		t.Fatalf("a group block: %q", p)
	}
}

func TestChatPromptIsBounded(t *testing.T) {
	v := Voice{Name: "Team", ReleasedCard: strings.Repeat("card ", 1000), Card: "DRAFT CARD",
		Exemplars:      []Exemplar{{Text: strings.Repeat("eins ", 200)}, {Text: "zwei"}, {Text: "drei"}},
		DraftExemplars: []Exemplar{{Text: "DRAFT PASSAGE"}}}
	p := ChatPrompt(v)
	if strings.Contains(p, "DRAFT") {
		t.Fatal("only what acts goes into the chat: no draft card, no draft passages")
	}
	if strings.Contains(p, "drei") || !strings.Contains(p, "zwei") {
		t.Fatalf("at most two passages: %q", p)
	}
	if n := len([]rune(p)); n > chatCardMax+2*chatExemplarChar+400 {
		t.Fatalf("the chat block runs to %d characters", n)
	}
	if ChatPrompt(Voice{Name: "leer"}) != "" {
		t.Fatal("a voice with nothing released adds nothing")
	}
}

func TestTaskOccasion(t *testing.T) {
	cases := []struct {
		conv        bool
		title, body string
		want        Occasion
	}{
		{true, "Reply to Globex", "occasion: publications", OccasionChat},
		{false, "Reply to Globex", "", OccasionCustomers},
		{false, "Write the September post", "Draft it.\noccasion: publications\n", OccasionPublications},
		{false, "x", "  Occasion: Chat  ", OccasionChat},
		{false, "x", "the occasion: publications is mentioned in passing", OccasionCustomers},
	}
	for _, c := range cases {
		if got := TaskOccasion(c.conv, c.title, c.body); got != c.want {
			t.Errorf("TaskOccasion(%v, %q, %q) = %q, want %q", c.conv, c.title, c.body, got, c.want)
		}
	}
	if OutwardOccasion(OccasionChat) != OccasionCustomers || OutwardOccasion(OccasionPublications) != OccasionPublications {
		t.Fatal("outward occasion")
	}
	if _, err := ParseOccasion("tender"); err == nil {
		t.Fatal("an unknown occasion has to be refused")
	}
	if o, err := ParseOccasion(" Customers "); err != nil || o != OccasionCustomers {
		t.Fatalf("%q %v", o, err)
	}
}
