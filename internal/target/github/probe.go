package github

import (
	"context"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Probe reads the account behind the token. `/user` costs one read against the
// rate limit, changes nothing, and answers the question the setup assistant
// asks: is this token alive, and whose is it.
func (System) Probe(ctx context.Context, cred target.Credential) (string, error) {
	var me struct {
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := NewClient(cred.BaseURL, cred.Token).do(ctx, "GET", "/user", nil, &me); err != nil {
		return "", err
	}
	if me.Login == "" {
		return me.Name, nil
	}
	if me.Name != "" {
		return me.Name + " (@" + me.Login + ")", nil
	}
	return "@" + me.Login, nil
}
