package githubapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// The shared App is public: any GitHub account can install it. The
// installation id that comes back through the browser therefore proves
// nothing — someone who learns another account's installation id could bind
// it to their own organisation and act in someone else's repositories. The
// shared App asks for user authorisation during installation; covey exchanges
// the code for the installing user's token and accepts the installation only
// if that user can see it (GET /user/installations).

// WebURL is github.com, where the OAuth token endpoint lives.
const WebURL = "https://github.com"

// ExchangeCode turns the OAuth code GitHub appended to the redirect into a
// user-to-server token.
func (c *Client) ExchangeCode(ctx context.Context, web, clientID, clientSecret, code string) (string, error) {
	if web == "" {
		web = WebURL
	}
	if clientID == "" || clientSecret == "" {
		return "", errors.New("the shared GitHub App has no OAuth client configured (COVEY_GITHUB_APP_CLIENT_ID/_SECRET)")
	}
	form := url.Values{"client_id": {clientID}, "client_secret": {clientSecret}, "code": {code}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(web, "/")+"/login/oauth/access_token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		Token string `json:"access_token"`
		Error string `json:"error_description"`
	}
	if err := decodeJSON(res, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		if out.Error == "" {
			out.Error = "no token"
		}
		return "", fmt.Errorf("GitHub OAuth: %s", out.Error)
	}
	return out.Token, nil
}

// UserCanSee reports whether the user behind a user-to-server token can access
// the installation of the given App.
func (c *Client) UserCanSee(ctx context.Context, api, userToken string, appID, installationID int64) (bool, error) {
	if api == "" {
		api = DefaultAPI
	}
	var out struct {
		Installations []struct {
			ID    int64 `json:"id"`
			AppID int64 `json:"app_id"`
		} `json:"installations"`
	}
	if err := c.call(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/user/installations?per_page=100", "Bearer "+userToken, nil, &out); err != nil {
		return false, err
	}
	for _, in := range out.Installations {
		if in.ID == installationID && in.AppID == appID {
			return true, nil
		}
	}
	return false, nil
}
