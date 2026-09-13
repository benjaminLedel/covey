package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/dream"
	"covey/internal/llm"
	"covey/internal/memory"
)

// fakeModel answers what a test tells it to, and records what it was asked.
// The dream is the one thing in covey that writes unasked, at night, with
// nobody watching — so what it does has to be reproducible without a provider.
type fakeModel struct {
	answers []string
	asked   []llm.Request
	err     error
}

func (f *fakeModel) Name() string { return "fake" }

func (f *fakeModel) Complete(_ context.Context, req llm.Request) (string, error) {
	f.asked = append(f.asked, req)
	if f.err != nil {
		return "", f.err
	}
	if len(f.answers) == 0 {
		return "", nil
	}
	out := f.answers[0]
	f.answers = f.answers[1:]
	return out, nil
}

// A page titled after an incident is what the dream is for. It renames it to
// the thing the page is about, records the title it had before, and the rename
// can be taken back in the morning.
func TestDreamRenamesAndCanBeUndone(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("traeumer")

	if _, err := s.mem.Write(ctx, agent.ID, memory.PageInput{
		Slug: "stoerung-meier", Title: "Störung bei Meier am 30.07.2026",
		Body:   "Der Kunde Meier meldete einen Ausfall der Schnittstelle. Ursache war ein abgelaufenes Zertifikat.",
		Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{answers: []string{
		`{"proposals":[{"slug":"stoerung-meier","title":"Kunde Meier","reason":"nennt die Sache statt des Vorfalls"}]}`,
		"Es träumte von einem Kunden, der endlich seinen Namen zurückbekam.",
	}}

	d, err := s.dreams.Begin(ctx, agent.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	s.dreams.Run(ctx, d, model)

	list, err := s.dreams.List(ctx, agent.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d dreams, expected 1", len(list))
	}
	done := list[0]
	if done.Status != "done" {
		t.Fatalf("the dream ended as %q: %s", done.Status, done.Error)
	}
	if done.Story == "" {
		t.Error("the dream tells nothing of itself")
	}

	var retitle dream.Action
	for _, a := range done.Actions {
		if a.Kind == "retitle" {
			retitle = a
		}
	}
	if retitle.ID.String() == "" || retitle.After != "Kunde Meier" {
		t.Fatalf("no rename was recorded: %+v", done.Actions)
	}
	if retitle.Before != "Störung bei Meier am 30.07.2026" {
		t.Errorf("the state before was not kept: %q", retitle.Before)
	}
	if retitle.Reason == "" {
		t.Error("the rename gives no reason — a dream has to be readable afterwards")
	}

	page, err := s.mem.Read(ctx, agent.ID, "stoerung-meier")
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Kunde Meier" {
		t.Errorf("the page is still called %q", page.Title)
	}

	// Undone individually: that is what makes writing unsupervised acceptable.
	if err := s.dreams.Undo(ctx, s.orgID, retitle.ID); err != nil {
		t.Fatalf("undo: %v", err)
	}
	page, err = s.mem.Read(ctx, agent.ID, "stoerung-meier")
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Störung bei Meier am 30.07.2026" {
		t.Errorf("the undo did not restore the title: %q", page.Title)
	}
	// A second undo is not an error but a no-op: the title is already back,
	// and two people clicking it in the morning must not fight over it.
	if err := s.dreams.Undo(ctx, s.orgID, retitle.ID); err != nil {
		t.Errorf("a second undo was refused: %v", err)
	}
	page, err = s.mem.Read(ctx, agent.ID, "stoerung-meier")
	if err != nil {
		t.Fatal(err)
	}
	if page.Title != "Störung bei Meier am 30.07.2026" {
		t.Errorf("the second undo changed the title again: %q", page.Title)
	}
	// A foreign organisation's action is not undoable from here at all.
	if err := s.dreams.Undo(ctx, uuid.New(), retitle.ID); err == nil {
		t.Error("an action of another organisation was undone")
	}
}

// An agent whose pages already name things has nothing to dream about, and
// that has to end as a finished dream rather than as an error.
func TestDreamWithNothingToDo(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("nichts-zu-tun")

	if _, err := s.mem.Write(ctx, agent.ID, memory.PageInput{
		Slug: "kunde-meier", Title: "Kunde Meier",
		Body: "Meier bezieht seit 2024 die Wartung. Ansprechpartnerin ist Frau Zabel.", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{}
	d, err := s.dreams.Begin(ctx, agent.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	s.dreams.Run(ctx, d, model)

	list, _ := s.dreams.List(ctx, agent.ID, 10)
	if len(list) != 1 || list[0].Status != "done" {
		t.Fatalf("the dream ended as %+v", list)
	}
	if len(model.asked) != 0 {
		t.Errorf("the model was asked although there was nothing to rename: %d calls", len(model.asked))
	}
}

// A model that does not answer must not leave the dream "running" forever —
// an agent that shows as dreaming and is not is the state nobody can act on.
func TestDreamRecordsAFailedModelCall(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("fehlschlag")

	if _, err := s.mem.Write(ctx, agent.ID, memory.PageInput{
		Slug: "vorfall", Title: "Ausfall am 12.09.2026 behoben",
		Body: "Die Schnittstelle war zwei Stunden weg. Ursache: ein Zertifikat.", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	model := &fakeModel{err: context.DeadlineExceeded}
	d, err := s.dreams.Begin(ctx, agent.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	s.dreams.Run(ctx, d, model)

	list, _ := s.dreams.List(ctx, agent.ID, 10)
	if len(list) != 1 {
		t.Fatalf("got %d dreams", len(list))
	}
	if list[0].Status != "error" {
		t.Errorf("a failed model call ended as %q", list[0].Status)
	}
	if list[0].Error == "" {
		t.Error("the failed dream says nothing about why")
	}
}

// Two dreams at once would be two writers on one memory. The second is
// refused, and the refusal is its own error so the nightly run can pass over
// it without making noise.
func TestOnlyOneDreamAtATime(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("doppelt")

	if _, err := s.dreams.Begin(ctx, agent.ID, "manual"); err != nil {
		t.Fatal(err)
	}
	_, err := s.dreams.Begin(ctx, agent.ID, "nightly")
	if err == nil {
		t.Fatal("a second dream was started beside the first")
	}

	current, running, err := s.dreams.Current(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !running || current.Status != "running" {
		t.Errorf("the running dream is not reported as such: %+v, %v", current, running)
	}
}

// The nightly run picks the agents that have slept since the dream hour. An
// agent that has been awake since is not among them.
func TestSleepersSince(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	s.newSupportAgent("schlaefer")

	ids, err := s.dreams.SleepersSince(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_ = ids

	// A moment in the future has nobody behind it.
	future, err := s.dreams.SleepersSince(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(future) != 0 {
		t.Errorf("agents are reported as having slept since a moment still to come: %v", future)
	}
}

// The story is ornament: a dream whose narration fails is still a successful
// dream, because memory upkeep must not fall over a sentence.
func TestDreamSurvivesAFailedStory(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("ohne-geschichte")

	if _, err := s.mem.Write(ctx, agent.ID, memory.PageInput{
		Slug: "vorfall-zwei", Title: "Störung am 01.08.2026",
		Body: "Der Dienst war kurz weg. Es lag am Speicher.", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	// First call answers the rename, second (the story) fails.
	model := &failSecond{first: `{"proposals":[{"slug":"vorfall-zwei","title":"Speicherengpass","reason":"benennt die Sache"}]}`}
	d, err := s.dreams.Begin(ctx, agent.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	s.dreams.Run(ctx, d, model)

	list, _ := s.dreams.List(ctx, agent.ID, 10)
	if len(list) != 1 || list[0].Status != "done" {
		t.Fatalf("the dream ended as %+v", list)
	}
	if list[0].Story != "" {
		t.Errorf("a story arrived although the call failed: %q", list[0].Story)
	}
	if len(list[0].Actions) == 0 {
		t.Error("the rename was lost with the story")
	}
}

type failSecond struct {
	first string
	calls int
}

func (f *failSecond) Name() string { return "fake" }

func (f *failSecond) Complete(_ context.Context, _ llm.Request) (string, error) {
	f.calls++
	if f.calls == 1 {
		return f.first, nil
	}
	return "", context.DeadlineExceeded
}
