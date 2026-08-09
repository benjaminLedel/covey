package gitlab

import (
	"context"

	"covey/internal/target"
)

// Probe reads the bot user behind the token. `/user` is GitLab's cheapest
// read, it changes nothing, and its failure modes are exactly the ones worth
// naming: wrong instance URL, expired token, token without the api scope.
//
// The name comes back as `@username`, because that is how the agent will
// appear under every issue comment it writes — whoever sees it here recognises
// it there.
func (System) Probe(ctx context.Context, cred target.Credential) (string, error) {
	var me struct {
		Username string `json:"username"`
		Name     string `json:"name"`
	}
	if err := NewClient(cred.BaseURL, cred.Token).do(ctx, "GET", "/user", nil, &me); err != nil {
		return "", err
	}
	if me.Username == "" {
		return me.Name, nil
	}
	if me.Name != "" {
		return me.Name + " (@" + me.Username + ")", nil
	}
	return "@" + me.Username, nil
}
