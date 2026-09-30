package chat

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestDirectKeyIsOnePerPair(t *testing.T) {
	a, b := Human(uuid.New()), Agent(uuid.New())
	if DirectKey(a, b) != DirectKey(b, a) {
		t.Fatal("A→B and B→A are two keys")
	}
}

// TestAddressed: in a direct conversation the agent answers everything; in a
// group only what is addressed to it — by @slug or @name, or as a reply to
// its own line — and "@ada" is not "@adam".
func TestAddressed(t *testing.T) {
	ada := Member{Kind: MemberAgent, ID: uuid.New(), Name: "Ada Lovelace", Slug: "ada"}
	adam := Member{Kind: MemberAgent, ID: uuid.New(), Name: "Adam", Slug: "adam"}
	mensch := Member{Kind: MemberHuman, ID: uuid.New(), Name: "Bea"}
	direkt := Conversation{Kind: KindDirect, Members: []Member{mensch, ada}}
	if got := Addressed(direkt, "hallo", nil); len(got) != 1 || got[0].ID != ada.ID {
		t.Fatalf("direct: %v", got)
	}
	gruppe := Conversation{Kind: KindGroup, Members: []Member{mensch, ada, adam}}
	faelle := []struct {
		text string
		will []uuid.UUID
	}{
		{"hallo zusammen", nil},
		{"@ada schau mal", []uuid.UUID{ada.ID}},
		{"@Adam, du auch", []uuid.UUID{adam.ID}},
		{"@ada-x ist jemand anderes", nil},
		{"frag @Ada Lovelace und @adam", []uuid.UUID{ada.ID, adam.ID}},
		{"mail an ada@example.org", nil},
	}
	for _, f := range faelle {
		got := Addressed(gruppe, f.text, nil)
		if len(got) != len(f.will) {
			t.Fatalf("%q: %v", f.text, got)
		}
		for i := range got {
			if got[i].ID != f.will[i] {
				t.Fatalf("%q: %v", f.text, got)
			}
		}
	}
	seine := Message{AuthorKind: MemberAgent, AuthorID: &adam.ID}
	if got := Addressed(gruppe, "danke", &seine); len(got) != 1 || got[0].ID != adam.ID {
		t.Fatalf("a reply addresses the author: %v", got)
	}
	weg := time.Now()
	gruppe.Members[1].LeftAt = &weg
	if got := Addressed(gruppe, "@ada?", nil); len(got) != 0 {
		t.Fatalf("an agent that left is not addressed: %v", got)
	}
}

// TestReportMessageOfAChatAnswer: the reply of a chat answer is a plain
// message that still names the outcome it reports (#483); its error, and
// every other task's outcome, keep their kind.
func TestReportMessageOfAChatAnswer(t *testing.T) {
	r := Report{TaskID: uuid.New(), AgentID: uuid.New(), ConversationID: uuid.New(), State: "done", ChatAnswer: true}
	m := r.Message("Hi!", map[string]string{"voice": "Warm"})
	if m.Kind != MessageText || m.Meta[ReportsMeta] != MessageResult || m.Meta["voice"] != "Warm" {
		t.Fatalf("chat answer: %+v", m)
	}
	if m.ID != ReportID(r.TaskID, MessageResult) {
		t.Fatal("the id has to stay the report's, so a second round writes nothing")
	}
	r.State = "failed"
	if m := r.Message("boom", nil); m.Kind != MessageError || m.Meta != nil {
		t.Fatalf("a failed chat answer stays an error: %+v", m)
	}
	r.State, r.ChatAnswer = "done", false
	if m := r.Message("report", nil); m.Kind != MessageResult {
		t.Fatalf("a task keeps its result kind: %+v", m)
	}
}
