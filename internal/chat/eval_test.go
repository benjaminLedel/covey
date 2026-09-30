package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

/* The evaluation set for conversations (#457).
 *
 * Unit tests pin what the code does with an answer; they cannot say whether
 * the answer sounds like a colleague. The set is the measure for that: a
 * scenario is one moment in a team chat — who is in it, who writes, what the
 * agent is working on, what was said — with the action the triage should
 * choose and the hard checks its text has to pass: the language, the length,
 * no talk about tasks and runs, no headings, the first name in a group, the
 * tone the organisation set.
 *
 * Two ways to run it:
 *
 *   - `go test`: the checks against answers recorded beside the scenarios,
 *     plus a set of deliberately bad answers that the checks must catch. That
 *     keeps the checks honest — a check nothing can fail is furniture.
 *   - `make eval-chat` (COVEY_EVAL_LLM=1): the same scenarios through the real
 *     Triagieren and Erzaehlen against the real model, the same checks, and a
 *     judge model's "sounds like a colleague" score (eval_live_test.go).
 *
 * The README in testdata/eval says how to add a scenario. */

const evalDir = "testdata/eval"

type evalTone struct {
	Address string `json:"address"`
	Tone    string `json:"tone"`
	Emoji   string `json:"emoji"`
	Note    string `json:"note"`
}

// evalRahmen mirrors Rahmen field for field; the bridge converts.
type evalRahmen struct {
	Rolle, Seele string
	Gegenueber   string
	Raum         string
	Ton          string
	// The chat voice and the audience lines of #471 (evalStimme).
	Stimme, Publikum string
	// The config proposals of #491: offered or not, and the heartbeat.
	Vorschlaege bool
	Takt        string
}

// evalVoice is a voice of the scenario's library, as far as the chat reads
// it: the released card, passages, and its chat tone.
type evalVoice struct {
	Card     string   `json:"card"`
	Passages []string `json:"passages"`
	ChatTone evalTone `json:"chat_tone"`
}

// evalDepartment is a department of the org chart with its "how to speak
// with us" line and the voice it names per occasion (#471).
type evalDepartment struct {
	Name   string            `json:"name"`
	Note   string            `json:"note"`
	Voices map[string]string `json:"voices"`
}

type evalScenario struct {
	Name     string `json:"name"`
	Language string `json:"language"`
	// Kind: triage | narration | addressing.
	Kind  string `json:"kind"`
	About string `json:"about"`

	Conversation struct {
		Kind    string `json:"kind"` // direct | group
		Title   string `json:"title"`
		Members []struct {
			Name string `json:"name"`
			Kind string `json:"kind"` // human | agent
			Slug string `json:"slug"`
			// Department of a human member: in a group, the others whose
			// departments the rule counts (#471).
			Department string `json:"department"`
		} `json:"members"`
	} `json:"conversation"`
	Person struct {
		Name             string `json:"name"`
		JobTitle         string `json:"job_title"`
		Department       string `json:"department"`
		Responsibilities string `json:"responsibilities"`
		// Technical is what the scenario is about, not something the turn
		// gets: the turn reads the job title, as it does in covey.
		Technical bool `json:"technical"`
	} `json:"person"`
	Agent struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
		Role string `json:"role"`
		Soul string `json:"soul"`
	} `json:"agent"`
	// Tone is the organisation's chat tone; the chosen voice's own chat tone
	// goes over it field by field, as in covey.
	Tone evalTone `json:"tone"`
	// Voices is the library and the slots of #471 by voice name: the agent's
	// and the organisation's, per occasion. Departments carry theirs.
	Voices struct {
		Library map[string]evalVoice `json:"library"`
		Agent   map[string]string    `json:"agent"`
		Org     map[string]string    `json:"org"`
	} `json:"voices"`
	Departments []evalDepartment `json:"departments"`
	OrgChart    string           `json:"org_chart"`
	OpenTasks   []struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
		Age    string `json:"age"`
	} `json:"open_tasks"`
	FinishedTasks []struct {
		Title   string `json:"title"`
		Outcome string `json:"outcome"`
		Age     string `json:"age"`
		Result  string `json:"result"`
	} `json:"finished_tasks"`
	History []struct {
		Who  string `json:"who"`
		Text string `json:"text"`
	} `json:"history"`
	Message string `json:"message"`
	// Task is what a narration tells: the job and how it ended.
	Task struct {
		Title  string `json:"title"`
		Body   string `json:"body"`
		State  string `json:"state"`
		Result string `json:"result"`
	} `json:"task"`
	SearchHits []string `json:"search_hits"`
	// ConfigProposals: the organisation has the trial of #491 on, so the
	// triage may choose "config"; Heartbeat is the agent's HEARTBEAT.md.
	ConfigProposals bool   `json:"config_proposals"`
	Heartbeat       string `json:"heartbeat"`

	Expect struct {
		Action    []string `json:"action"`
		Addressed *bool    `json:"addressed"`
		// Voice and VoiceReason: what the rule of #471 has to choose for the
		// chat ("" and "none×chat" when no level names one); Audience the
		// departments whose lines come along, in order. Checked offline.
		Voice       string   `json:"voice"`
		VoiceReason string   `json:"voice_reason"`
		Audience    []string `json:"audience"`
	} `json:"expect"`
	Checks struct {
		FirstName      bool     `json:"first_name"`
		MaxSentences   int      `json:"max_sentences"`
		MaxChars       int      `json:"max_chars"`
		MustContain    []string `json:"must_contain"`
		MustContainAny []string `json:"must_contain_any"`
		MustNotContain []string `json:"must_not_contain"`
	} `json:"checks"`
	// Recorded is a good answer as the model gives it: the raw triage output,
	// or the narrated chat line.
	Recorded string `json:"recorded"`
}

type evalBad struct {
	Scenario string   `json:"scenario"`
	Why      string   `json:"why"`
	Recorded string   `json:"recorded"`
	Fails    []string `json:"fails"`
}

func ladeSzenarien(t *testing.T) []evalScenario {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(evalDir, "scenarios", "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scenarios: %v", err)
	}
	var out []evalScenario
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var sc evalScenario
		if err := json.Unmarshal(raw, &sc); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if want := strings.TrimSuffix(filepath.Base(f), ".json"); sc.Name != want {
			t.Fatalf("%s: name %q does not match the file", f, sc.Name)
		}
		out = append(out, sc)
	}
	return out
}

// agentID is stable per scenario, so a group lists the same members every run.
func (sc evalScenario) agentID() uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("eval-agent:"+sc.Agent.Slug))
}

func (sc evalScenario) gespraech() Conversation {
	c := Conversation{Kind: KindDirect, Title: sc.Conversation.Title}
	if sc.Conversation.Kind == KindGroup {
		c.Kind = KindGroup
	}
	c.Members = append(c.Members, Member{Kind: MemberAgent, ID: sc.agentID(), Name: sc.Agent.Name, Slug: sc.Agent.Slug})
	if c.Kind == KindDirect {
		c.Members = append(c.Members, Member{Kind: MemberHuman, ID: uuid.New(), Name: sc.Person.Name})
	}
	for _, m := range sc.Conversation.Members {
		kind := MemberHuman
		if m.Kind == MemberAgent {
			kind = MemberAgent
		}
		c.Members = append(c.Members, Member{Kind: kind, ID: uuid.NewSHA1(uuid.NameSpaceOID, []byte("eval:"+m.Name)), Name: m.Name, Slug: m.Slug})
	}
	return c
}

func (sc evalScenario) gruppe() bool { return sc.Conversation.Kind == KindGroup }

// gegenueber reads like the description covey builds from the org chart.
func (sc evalScenario) gegenueber() string {
	teile := []string{sc.Person.Name}
	if sc.Person.JobTitle != "" {
		teile = append(teile, "job title: "+sc.Person.JobTitle)
	}
	if sc.Person.Department != "" {
		teile = append(teile, "department: "+sc.Person.Department)
	}
	if sc.Person.Responsibilities != "" {
		teile = append(teile, "responsible for: "+sc.Person.Responsibilities)
	}
	return strings.Join(teile, " — ")
}

func (sc evalScenario) rahmen() evalRahmen {
	w := evalStimme(sc)
	return evalRahmen{
		Rolle: sc.Agent.Role, Seele: sc.Agent.Soul, Gegenueber: sc.gegenueber(),
		Raum: evalRaum(sc.gespraech(), sc.agentID(), sc.Person.Name, sc.Kind == "triage"),
		Ton:  w.Ton, Stimme: w.Stimme, Publikum: w.Publikum,
		Vorschlaege: sc.ConfigProposals, Takt: sc.Heartbeat,
	}
}

// evalWahl is what the rule of #471 made of a scenario: the chat voice and
// why, the lines of the departments involved, and the chat tone in effect.
type evalWahl struct {
	Stimme, Publikum string
	Name, Grund      string
	Abteilungen      []string
	Ton              string
	Effektiv         evalTone
}

// abteilung is the scenario's department of that name — one without a line
// or voices when the scenario does not describe it: in covey a person's
// department always counts, whether it said anything or not.
func (sc evalScenario) abteilung(name string) (evalDepartment, bool) {
	if strings.TrimSpace(name) == "" {
		return evalDepartment{}, false
	}
	for _, d := range sc.Departments {
		if d.Name == name {
			return d, true
		}
	}
	return evalDepartment{Name: name}, true
}

func (sc evalScenario) verlauf() []Beitrag {
	var out []Beitrag
	for _, h := range sc.History {
		out = append(out, Beitrag{Wer: h.Who, Text: h.Text})
	}
	return out
}

func (sc evalScenario) offen() []Offen {
	var out []Offen
	for _, o := range sc.OpenTasks {
		out = append(out, Offen{Kurz: o.ID, Titel: o.Title, Status: o.Status, Alter: o.Age})
	}
	return out
}

func (sc evalScenario) fertig() []Fertig {
	var out []Fertig
	for _, f := range sc.FinishedTasks {
		out = append(out, Fertig{Titel: f.Title, Ausgang: f.Outcome, Alter: f.Age, Ergebnis: f.Result})
	}
	return out
}

// evalAusgabe is what came out of one scenario, recorded or live.
type evalAusgabe struct {
	// Aktion is the triage's first decision; Endgueltig the one after a search.
	Aktion, Endgueltig Aktion
	Text               string
	Angesprochen       *bool
	Fehler             error
}

// ausgabe is what a decision says in the conversation: the text of an
// answer or of a task's acknowledgement, the reply of a note (#460).
func ausgabe(e Entscheidung) evalAusgabe {
	text := e.Text
	if e.Aktion == AktionNotiz {
		text = e.Antwort
	}
	// A config decision's text names who may accept through a placeholder
	// the server fills (#491); the checks read it with a name in it.
	if e.Aktion == AktionKonfig {
		text = strings.ReplaceAll(text, ApproversPlatzhalter, "Bernd")
	}
	return evalAusgabe{Aktion: e.Aktion, Endgueltig: e.Aktion, Text: text}
}

// aufgezeichnet reads a recorded answer the way covey reads the model's.
func aufgezeichnet(sc evalScenario, roh string) evalAusgabe {
	switch sc.Kind {
	case "addressing":
		return evalAusgabe{Angesprochen: angesprochen(sc)}
	case "narration":
		return evalAusgabe{Text: strings.TrimSpace(roh)}
	}
	e, err := lesen(roh)
	if err != nil {
		return evalAusgabe{Fehler: err}
	}
	return ausgabe(e)
}

func angesprochen(sc evalScenario) *bool {
	ja := slices.ContainsFunc(Addressed(sc.gespraech(), sc.Message, nil), func(m Member) bool { return m.ID == sc.agentID() })
	return &ja
}

// befund is one failed check: which, and what was seen.
type befund struct{ Check, Detail string }

func (b befund) String() string { return b.Check + ": " + b.Detail }

// metaPhrasen are the words of the machinery, and of a service desk: a
// colleague answers, it does not report on answering.
var metaPhrasen = []string{
	"aufgabe beantwortet", "begrüßung beantwortet", "frage beantwortet",
	"i have answered", "i've answered", "i answered your",
	"the task", "the run", "the result", "the report", "the record",
	"die aufgabe", "der lauf", "den lauf", "des laufs", "der bericht", "den bericht", "das protokoll",
	"as an ai", "als ki", "ki-assistent", "ai assistant", "how may i assist",
}

var (
	metaHabe  = regexp.MustCompile(`(?i)\b(ich habe|habe ich|hab|i have|i've)\b[^.!?]{0,60}(beantwortet|erklärt|answered|explained)`)
	markdown  = regexp.MustCompile(`(?m)^\s*(#{1,6}\s|[-*•]\s|\d+\.\s)|\*\*|__`)
	duFormen  = regexp.MustCompile(`(?i)\b(du|dir|dich|dein|deine|deinen|deinem|deiner|deins)\b`)
	sieFormen = regexp.MustCompile(`\b(Ihnen|Ihrem|Ihren|Ihrer)\b`)
	sieImText = regexp.MustCompile(`\b(Sie|Ihnen|Ihr|Ihre)\b`)
)

var stoppwoerter = map[string][]string{
	"de": {"der", "die", "das", "und", "ist", "ich", "du", "nicht", "ein", "eine", "mit", "auf", "für", "wir", "es", "hab", "habe", "schon", "noch", "gerade", "zu", "bei", "dir", "dich", "mal", "sie", "war", "doch", "gern", "läuft", "wieder"},
	"en": {"the", "and", "is", "i", "you", "not", "a", "an", "with", "on", "for", "we", "it", "have", "has", "just", "still", "right", "to", "at", "me", "your", "been", "that", "was", "are", "all", "of"},
}

// sprache guesses the language from function words; "" when too short to say.
func sprache(s string) string {
	zaehl := map[string]int{}
	woerter := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) })
	if len(woerter) < 4 {
		return ""
	}
	for _, w := range woerter {
		for lang, liste := range stoppwoerter {
			if slices.Contains(liste, w) {
				zaehl[lang]++
			}
		}
	}
	switch {
	case zaehl["de"] >= 2 && zaehl["de"] > zaehl["en"]:
		return "de"
	case zaehl["en"] >= 2 && zaehl["en"] > zaehl["de"]:
		return "en"
	}
	return ""
}

// saetze counts sentences: a run of letters ended by . ! ? or …; a point
// between digits (1.240) ends nothing.
func saetze(s string) int {
	r := []rune(s)
	n, drin := 0, false
	for i, c := range r {
		switch {
		case unicode.IsLetter(c) || unicode.IsDigit(c):
			drin = true
		case strings.ContainsRune(".!?…", c) && drin:
			if c == '.' && i+1 < len(r) && unicode.IsDigit(r[i+1]) {
				continue
			}
			n++
			drin = false
		}
	}
	if drin {
		n++
	}
	return n
}

// alsWort finds a phrase as whole words: "der lauf" is in "der Lauf ist
// durch" and not in "wieder laufen".
func alsWort(text, phrase string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], phrase)
		if j < 0 {
			return false
		}
		start, ende := i+j, i+j+len(phrase)
		vor, _ := utf8.DecodeLastRuneInString(text[:start])
		nach, _ := utf8.DecodeRuneInString(text[ende:])
		if (start == 0 || !unicode.IsLetter(vor)) && (ende == len(text) || !unicode.IsLetter(nach)) {
			return true
		}
		i = start + 1
	}
}

func istEmoji(c rune) bool {
	switch {
	case c >= 0x1F3FB && c <= 0x1F3FF: // skin tones modify, they are not one more
		return false
	case c >= 0x1F000 && c <= 0x1FAFF, c >= 0x2600 && c <= 0x27BF, c >= 0x2B00 && c <= 0x2BFF:
		return true
	}
	return false
}

func emojis(s string) int {
	n := 0
	for _, c := range s {
		if istEmoji(c) {
			n++
		}
	}
	return n
}

// pruefen runs the hard checks of a scenario over one outcome.
func pruefen(sc evalScenario, a evalAusgabe) []befund {
	var out []befund
	add := func(check, format string, args ...any) {
		out = append(out, befund{check, fmt.Sprintf(format, args...)})
	}

	if sc.Kind == "addressing" {
		if sc.Expect.Addressed != nil && (a.Angesprochen == nil || *a.Angesprochen != *sc.Expect.Addressed) {
			add("addressed", "want %v", *sc.Expect.Addressed)
		}
		return out
	}
	if a.Fehler != nil {
		add("parse", "%v", a.Fehler)
		return out
	}
	if sc.Kind == "triage" {
		if len(sc.Expect.Action) > 0 && !slices.Contains(sc.Expect.Action, string(a.Aktion)) &&
			!slices.Contains(sc.Expect.Action, string(a.Endgueltig)) {
			add("action", "got %s (then %s), want %v", a.Aktion, a.Endgueltig, sc.Expect.Action)
		}
		// Only what is said in the chat is checked as chat: a search says
		// nothing, and of a note only its reply reaches the conversation.
		if a.Endgueltig == AktionSuche {
			return out
		}
	}
	text := strings.TrimSpace(a.Text)
	if text == "" {
		switch {
		case a.Endgueltig == AktionNotiz:
			add("silent", "a note without a reply leaves the person without an answer (#460)")
		case sc.Kind == "narration" || a.Endgueltig == AktionAntwort || a.Endgueltig == AktionKonfig:
			add("empty", "nothing said")
		}
		return out
	}
	klein := strings.ToLower(text)

	if l := sprache(text); l != "" && sc.Language != "" && l != sc.Language {
		add("language", "reads as %s, want %s", l, sc.Language)
	}
	maxS, maxC := sc.Checks.MaxSentences, sc.Checks.MaxChars
	if maxS == 0 {
		maxS = map[bool]int{true: 3, false: 4}[sc.gruppe()]
		if sc.Kind == "narration" {
			maxS++
		}
	}
	if maxC == 0 {
		maxC = map[string]int{"triage": 400, "narration": 700}[sc.Kind]
	}
	if n := saetze(text); n > maxS {
		add("sentences", "%d, at most %d", n, maxS)
	}
	if n := len([]rune(text)); n > maxC {
		add("chars", "%d, at most %d", n, maxC)
	}
	for _, m := range metaPhrasen {
		if alsWort(klein, m) {
			add("meta", "%q", m)
		}
	}
	if m := metaHabe.FindString(text); m != "" {
		add("meta", "%q", m)
	}
	if m := markdown.FindString(text); m != "" {
		add("markdown", "%q", strings.TrimSpace(m))
	}
	if sc.Checks.FirstName {
		if vorname := strings.Fields(sc.Person.Name)[0]; !strings.Contains(klein, strings.ToLower(vorname)) {
			add("first_name", "%q not named", vorname)
		}
	}
	for _, m := range sc.Checks.MustContain {
		if !strings.Contains(klein, strings.ToLower(m)) {
			add("must_contain", "%q missing", m)
		}
	}
	if len(sc.Checks.MustContainAny) > 0 && !slices.ContainsFunc(sc.Checks.MustContainAny, func(m string) bool {
		return strings.Contains(klein, strings.ToLower(m))
	}) {
		add("must_contain", "none of %q", sc.Checks.MustContainAny)
	}
	for _, m := range sc.Checks.MustNotContain {
		if strings.Contains(klein, strings.ToLower(m)) {
			add("must_not_contain", "%q", m)
		}
	}
	ton := evalStimme(sc).Effektiv
	switch n := emojis(text); ton.Emoji {
	case "never":
		if n > 0 {
			add("emoji", "%d, the tone says never", n)
		}
	case "sparingly":
		if n > 1 {
			add("emoji", "%d, the tone says at most one", n)
		}
	}
	if sc.Language == "de" {
		anrede := ton.Address
		if anrede == "auto" {
			anrede = "du"
			if sieImText.MatchString(sc.Message) {
				anrede = "sie"
			}
		}
		switch anrede {
		case "sie":
			if m := duFormen.FindString(text); m != "" {
				add("address", "%q where the tone says Sie", m)
			}
		case "du":
			if m := sieFormen.FindString(text); m != "" {
				add("address", "%q where the tone says du", m)
			}
		}
	}
	return out
}

// TestEvalSzenarienSindVollstaendig: the set covers what #457 asks for —
// both languages, all three kinds, groups and direct conversations — and
// every scenario can be checked.
func TestEvalSzenarienSindVollstaendig(t *testing.T) {
	szenarien := ladeSzenarien(t)
	if len(szenarien) < 20 {
		t.Fatalf("%d scenarios, want at least 20", len(szenarien))
	}
	gesehen := map[string]bool{}
	for _, sc := range szenarien {
		gesehen[sc.Language] = true
		gesehen[sc.Kind] = true
		gesehen[sc.Conversation.Kind] = true
		switch sc.Kind {
		case "triage":
			if len(sc.Expect.Action) == 0 || sc.Recorded == "" || sc.Message == "" {
				t.Errorf("%s: a triage scenario needs a message, an expected action and a recorded answer", sc.Name)
			}
		case "narration":
			if sc.Task.Result == "" || sc.Recorded == "" {
				t.Errorf("%s: a narration scenario needs a result and a recorded answer", sc.Name)
			}
		case "addressing":
			if sc.Expect.Addressed == nil || !sc.gruppe() {
				t.Errorf("%s: an addressing scenario is a group message with expect.addressed", sc.Name)
			}
		default:
			t.Errorf("%s: unknown kind %q", sc.Name, sc.Kind)
		}
		if sc.Language != "de" && sc.Language != "en" {
			t.Errorf("%s: language %q", sc.Name, sc.Language)
		}
	}
	for _, will := range []string{"de", "en", "triage", "narration", "addressing", KindGroup, KindDirect} {
		if !gesehen[will] {
			t.Errorf("no scenario of %q", will)
		}
	}
	// The voices of #471: a department's voice, the agent's chat voice beside
	// a customers voice, a group whose departments' lines combine, and a chat
	// that no level names a voice for while the agent carries a customers one.
	faelle := map[string]bool{}
	for _, sc := range szenarien {
		switch r := sc.Expect.VoiceReason; {
		case strings.HasPrefix(r, "department:"):
			faelle["department"] = true
		case r == "agent×chat" && sc.Voices.Agent["customers"] != "":
			faelle["chat beside customers"] = true
		case (r == "" || r == "none×chat") && sc.Voices.Agent["customers"] != "":
			faelle["none, customers tone"] = true
		}
		if sc.gruppe() && len(sc.Expect.Audience) > 1 {
			faelle["mixed group"] = true
		}
		if !sc.gruppe() && len(sc.Expect.Audience) == 1 {
			faelle["audience line"] = true
		}
	}
	for _, will := range []string{"department", "chat beside customers", "none, customers tone", "mixed group", "audience line"} {
		if !faelle[will] {
			t.Errorf("no voice scenario of %q (#471)", will)
		}
	}
}

// TestEvalStimmen: the voice of #471 each scenario's chat speaks in — the
// rule's choice and its reason, the departments whose lines come along — and
// that both reach the turn's frame. A scenario without voices speaks in none,
// and nothing of a voice is in its frame.
func TestEvalStimmen(t *testing.T) {
	for _, sc := range ladeSzenarien(t) {
		if sc.Kind == "addressing" {
			continue
		}
		t.Run(sc.Name, func(t *testing.T) {
			w := evalStimme(sc)
			grund := sc.Expect.VoiceReason
			if grund == "" {
				grund = "none×chat"
			}
			if w.Grund != grund || w.Name != sc.Expect.Voice {
				t.Fatalf("voice %q (%s), want %q (%s)", w.Name, w.Grund, sc.Expect.Voice, grund)
			}
			if strings.Join(w.Abteilungen, ",") != strings.Join(sc.Expect.Audience, ",") {
				t.Fatalf("audience %v, want %v", w.Abteilungen, sc.Expect.Audience)
			}
			var b strings.Builder
			Rahmen(sc.rahmen()).schreiben(&b)
			frame := b.String()
			if sc.Expect.Voice == "" {
				if strings.Contains(frame, "Passages in this voice") || strings.Contains(frame, "which your organisation chose") {
					t.Fatalf("no voice applies, yet the frame carries one:\n%s", frame)
				}
			} else if !strings.Contains(frame, fmt.Sprintf("the voice %q", sc.Expect.Voice)) {
				t.Fatalf("the chosen voice is not in the frame:\n%s", frame)
			}
			for name, v := range sc.Voices.Library {
				if card := strings.TrimSpace(v.Card); name != sc.Expect.Voice && card != "" && strings.Contains(frame, card) {
					t.Errorf("the card of %q, which the rule did not choose, is in the frame", name)
				}
			}
			for _, d := range sc.Departments {
				if note := strings.TrimSpace(d.Note); note != "" &&
					slices.Contains(sc.Expect.Audience, d.Name) != strings.Contains(frame, note) {
					t.Errorf("the line of %s: in the frame %v, expected %v", d.Name, strings.Contains(frame, note), slices.Contains(sc.Expect.Audience, d.Name))
				}
			}
		})
	}
}

// TestEvalAufgezeichnet: the recorded answers pass their own checks.
func TestEvalAufgezeichnet(t *testing.T) {
	for _, sc := range ladeSzenarien(t) {
		t.Run(sc.Name, func(t *testing.T) {
			for _, b := range pruefen(sc, aufgezeichnet(sc, sc.Recorded)) {
				t.Errorf("%s", b)
			}
		})
	}
}

// TestEvalFaengtSchlechteAntworten: every deliberately bad answer fails the
// checks it names. The observed reply of #457 is among them.
func TestEvalFaengtSchlechteAntworten(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(evalDir, "bad.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schlecht []evalBad
	if err := json.Unmarshal(raw, &schlecht); err != nil {
		t.Fatal(err)
	}
	nach := map[string]evalScenario{}
	for _, sc := range ladeSzenarien(t) {
		nach[sc.Name] = sc
	}
	for i, b := range schlecht {
		sc, ok := nach[b.Scenario]
		if !ok {
			t.Errorf("bad answer %d: no scenario %q", i, b.Scenario)
			continue
		}
		befunde := pruefen(sc, aufgezeichnet(sc, b.Recorded))
		for _, check := range b.Fails {
			if !slices.ContainsFunc(befunde, func(f befund) bool { return f.Check == check }) {
				t.Errorf("%s (%s): the check %q did not catch %q — found %v", b.Scenario, b.Why, check, b.Recorded, befunde)
			}
		}
	}
}

// TestEvalHilfsmittel pins the counting the checks rest on.
func TestEvalHilfsmittel(t *testing.T) {
	if n := saetze("Hey Ada 👋 Ich sitz an der Rechnung, 2 × 1.240 €. Sonst ruhig!"); n != 2 {
		t.Errorf("sentences: %d", n)
	}
	if n := emojis("Danke 👍🏽 und 🙌"); n != 2 {
		t.Errorf("emoji: %d", n)
	}
	if l := sprache("Ja, die ist gestern per Mail rausgegangen."); l != "de" {
		t.Errorf("language: %q", l)
	}
	if !alsWort("der lauf ist durch", "der lauf") || alsWort("sollte wieder laufen", "der lauf") {
		t.Error("meta phrases are matched as whole words")
	}
	if l := sprache("Yes, it went out yesterday to the customer."); l != "en" {
		t.Errorf("language: %q", l)
	}
}
