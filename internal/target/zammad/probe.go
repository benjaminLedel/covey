package zammad

import (
	"context"
	"strings"

	"covey/internal/target"
)

// Probe answers the only question that matters after storing a token: does it
// work, and as whom.
//
// `/users/me` is the cheapest honest answer Zammad has — it costs one read,
// changes nothing, and it fails for exactly the reasons that are worth
// reporting: wrong address, revoked token, token access switched off.
//
// The identity is deliberately the login and not the ID: whoever set the agent
// up in Zammad recognises the name they typed there, and a numeric ID would
// send them looking it up.
func (System) Probe(ctx context.Context, cred target.Credential) (string, error) {
	var me struct {
		Login     string `json:"login"`
		Email     string `json:"email"`
		Firstname string `json:"firstname"`
		Lastname  string `json:"lastname"`
	}
	if err := NewClient(cred.BaseURL, cred.Token).do(ctx, "GET", "/users/me", nil, &me); err != nil {
		return "", err
	}
	name := strings.TrimSpace(me.Firstname + " " + me.Lastname)
	switch {
	case me.Login != "" && name != "":
		return name + " (" + me.Login + ")", nil
	case me.Login != "":
		return me.Login, nil
	case me.Email != "":
		return me.Email, nil
	default:
		return "", nil
	}
}
