package chat

import (
	"strings"
	"testing"
)

// feed hands raw to a SpokenStream in pieces of n bytes and records what it
// emits, and after which byte.
func feed(raw string, n int) (out []string, at []int) {
	s := SpokenStream{Emit: func(sentence string) bool {
		out = append(out, sentence)
		return true
	}}
	for i := 0; i < len(raw); i += n {
		end := min(i+n, len(raw))
		before := len(out)
		s.Feed(raw[i:end])
		for range out[before:] {
			at = append(at, end)
		}
	}
	return out, at
}

func TestSpokenStreamHandsOverSentencesAsTheyComplete(t *testing.T) {
	raw := `{"action":"answer","spoken":"Ja, ist drin. Die Tests sind grün, und er wartet auf Adas Review.","text":"Ist drin — MR !88, Pipeline grün."}`
	for _, n := range []int{1, 3, 7, 64, len(raw)} {
		out, at := feed(raw, n)
		want := []string{"Ja, ist drin.", "Die Tests sind grün, und er wartet auf Adas Review."}
		if strings.Join(out, "|") != strings.Join(want, "|") {
			t.Fatalf("pieces of %d: %q", n, out)
		}
		if n == 1 {
			// The first once the second has begun; the second when the string closes.
			first := strings.Index(raw, "Die") + 1
			if at[0] != first {
				t.Errorf("first sentence after byte %d, want %d", at[0], first)
			}
			if closing := strings.Index(raw, `.","text"`) + 2; at[1] != closing {
				t.Errorf("second sentence after byte %d, want %d", at[1], closing)
			}
		}
	}
}

func TestSpokenStreamDecodesEscapesAcrossPieces(t *testing.T) {
	raw := `{"action":"task","spoken":"Mach ich — \"sofort\".\nIch meld mich 😀 im Chat.","title":"x"}`
	raw = strings.Replace(raw, "😀", `😀`, 1)
	out, _ := feed(raw, 1)
	if len(out) != 2 || out[0] != `Mach ich — "sofort".` || out[1] != "Ich meld mich 😀 im Chat." {
		t.Fatalf("%q", out)
	}
	// Multi-byte characters cut between pieces.
	out, _ = feed(`{"action":"answer","spoken":"Grüße aus Köln. Schön!"}`, 1)
	if strings.Join(out, "|") != "Grüße aus Köln.|Schön!" {
		t.Fatalf("%q", out)
	}
}

func TestSpokenStreamSpeaksOnlyAnswersNotesAndTasks(t *testing.T) {
	for _, raw := range []string{
		`{"action":"config","spoken":"Hab's entworfen, {approvers} kann es annehmen. Gut?","text":"x"}`,
		`{"action":"search","query":"Export"}`,
		`{"spoken":"Before the action. Not spoken.","action":"answer","text":"x"}`,
	} {
		if out, _ := feed(raw, 2); len(out) != 0 {
			t.Errorf("%s: %q", raw, out)
		}
	}
	out, _ := feed(`{"action":"note","task":"ab12","spoken":"Danke, nehm ich mit.","reply":"Danke, nehm ich mit."}`, 4)
	if len(out) != 1 || out[0] != "Danke, nehm ich mit." {
		t.Fatalf("%q", out)
	}
}

func TestSpokenStreamReadsOnlyTheObjectsOwnKey(t *testing.T) {
	raw := `Sure: {"action":"answer","text":"Write \"spoken\": \"no\". Done.","meta":{"spoken":"nested"},"spoken":"Klar. Mach ich."}`
	out, _ := feed(raw, 5)
	if strings.Join(out, "|") != "Klar.|Mach ich." {
		t.Fatalf("%q", out)
	}
}

func TestSpokenStreamKeepsWhatSprechbarKeeps(t *testing.T) {
	raw := `{"action":"answer","spoken":"Eins ist fertig. Zwei auch. Drei ist ein Angebot, das keiner wollte.","text":"x"}`
	out, _ := feed(raw, 3)
	if strings.Join(out, "|") != "Eins ist fertig.|Zwei auch." {
		t.Fatalf("at most %d sentences: %q", SpokenSentences, out)
	}
	// "z. B." ends no sentence, as in the stored form.
	out, _ = feed(`{"action":"answer","spoken":"Das geht z. B. über den Export. Fertig."}`, 1)
	if strings.Join(out, "|") != "Das geht z. B. über den Export.|Fertig." {
		t.Fatalf("%q", out)
	}
	// Emit saying stop ends it.
	var got []string
	s := SpokenStream{Emit: func(x string) bool { got = append(got, x); return false }}
	s.Feed(`{"action":"answer","spoken":"Eins. Zwei. Drei."}`)
	if len(got) != 1 {
		t.Fatalf("%q", got)
	}
}

// The end of the spoken form (#533): told once, when its string closed or
// as much as is kept was handed over; not when nothing was taken.
func TestSpokenStreamTellsTheEndOfTheSpokenForm(t *testing.T) {
	ends := 0
	var got []string
	s := SpokenStream{Emit: func(x string) bool { got = append(got, x); return true }, End: func() { ends++ }}
	raw := `{"action":"answer","spoken":"Ja. Ist drin.","text":"Ist drin."}`
	for i := range raw {
		s.Feed(raw[i : i+1])
		if i < strings.Index(raw, `.","text"`)+1 && ends != 0 {
			t.Fatalf("end told before the string closed, at byte %d", i)
		}
	}
	if ends != 1 || len(got) != 2 {
		t.Fatalf("ends %d, %q", ends, got)
	}
	ends = 0
	s = SpokenStream{Emit: func(string) bool { return true }, End: func() { ends++ }}
	s.Feed(`{"action":"answer","spoken":"Eins ist fertig. Zwei auch. Drei`)
	if ends != 1 {
		t.Fatalf("at %d sentences the spoken form is complete: ends %d", SpokenSentences, ends)
	}
	ends = 0
	s = SpokenStream{Emit: func(string) bool { return false }, End: func() { ends++ }}
	s.Feed(`{"action":"answer","spoken":"Yes, it is in."}`)
	if ends != 0 {
		t.Fatal("an end told for a spoken form nobody took")
	}
}
