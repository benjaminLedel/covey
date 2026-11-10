package teamsapp

import (
	"testing"

	"github.com/google/uuid"

	"covey/internal/agents"
)

func TestNamedPicksTheColleagueAtTheStart(t *testing.T) {
	vera := agents.Agent{ID: uuid.New(), Slug: "vera", DisplayName: "Vera Brandt"}
	veraB := agents.Agent{ID: uuid.New(), Slug: "vb", DisplayName: "Vera Brandt-Kolb"}
	paula := agents.Agent{ID: uuid.New(), Slug: "writer-1", DisplayName: "Paula"}
	list := []agents.Agent{vera, veraB, paula}
	cases := []struct {
		text, want, rest string
	}{
		{"Vera, check the invoice", "vera", "check the invoice"},
		{"vera: hi", "vera", "hi"},
		{"@Paula draft it", "writer-1", "draft it"},
		{"writer-1 — go", "writer-1", "go"},
		{"Vera Brandt-Kolb, you", "vb", "you"},
		{"Veronika, hi", "", "Veronika, hi"},
		{"hello Vera", "", "hello Vera"},
	}
	for _, c := range cases {
		got, rest := named(list, c.text)
		slug := ""
		if got != nil {
			slug = got.Slug
		}
		if slug != c.want || (got != nil && rest != c.rest) {
			t.Errorf("%q: got %q rest %q, want %q rest %q", c.text, slug, rest, c.want, c.rest)
		}
	}
}

func TestPickChannel(t *testing.T) {
	list := []ChannelRef{
		{Team: "Sales", Channel: "General", ChannelID: "19:s"},
		{Team: "Sales", Channel: "Leads", ChannelID: "19:sl"},
		{Team: "Support", Channel: "General", ChannelID: "19:p"},
	}
	if c, err := pickChannel(list, "Leads"); err != nil || c.ChannelID != "19:sl" {
		t.Fatalf("unique name: %+v %v", c, err)
	}
	if _, err := pickChannel(list, "General"); err == nil {
		t.Fatal("an ambiguous name must ask for Team/Channel")
	}
	if c, err := pickChannel(list, "support/general"); err != nil || c.ChannelID != "19:p" {
		t.Fatalf("qualified: %+v %v", c, err)
	}
	if c, err := pickChannel(list, "19:s"); err != nil || c.Channel != "General" {
		t.Fatalf("by id: %+v %v", c, err)
	}
	if _, err := pickChannel(list, "19:elsewhere"); err == nil {
		t.Fatal("a channel outside the list must be refused")
	}
}
