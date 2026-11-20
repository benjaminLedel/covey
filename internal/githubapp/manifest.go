package githubapp

import (
	"net/url"
	"strings"
)

// Permissions are the App's rights. They are the ceiling: what one agent may
// do is still decided by its ACCESS.md, and the plugin's scopes.
var Permissions = map[string]string{
	"metadata":      "read",
	"contents":      "write",
	"issues":        "write",
	"pull_requests": "write",
	"actions":       "read",
	"checks":        "read",
}

// Events are the webhook events the App subscribes to — what wakes an agent.
var Events = []string{"issues", "issue_comment", "pull_request", "pull_request_review", "pull_request_review_comment"}

// Manifest describes an App to GitHub's manifest flow
// (https://docs.github.com/apps/sharing-github-apps/registering-a-github-app-from-a-manifest).
type Manifest struct {
	Name               string            `json:"name"`
	URL                string            `json:"url"`
	Description        string            `json:"description,omitempty"`
	HookAttributes     HookAttributes    `json:"hook_attributes"`
	RedirectURL        string            `json:"redirect_url"`
	SetupURL           string            `json:"setup_url"`
	SetupOnUpdate      bool              `json:"setup_on_update"`
	Public             bool              `json:"public"`
	DefaultPermissions map[string]string `json:"default_permissions"`
	DefaultEvents      []string          `json:"default_events"`
}

// HookAttributes is the webhook part of a manifest.
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// NewManifest builds the manifest for an organisation's own App. publicURL is
// covey's COVEY_PUBLIC_URL; the redirect brings the browser back with the code,
// the setup URL with the installation.
func NewManifest(name, publicURL string) Manifest {
	base := strings.TrimRight(publicURL, "/")
	return Manifest{
		Name:               name,
		URL:                base,
		Description:        "covey agents: issues, pull requests and code review for this organisation's agents.",
		HookAttributes:     HookAttributes{URL: base + "/api/webhooks/github-app", Active: true},
		RedirectURL:        base + "/api/integrations/github/manifest",
		SetupURL:           base + "/api/integrations/github/installed",
		SetupOnUpdate:      true,
		Public:             false,
		DefaultPermissions: Permissions,
		DefaultEvents:      Events,
	}
}

// ManifestFormURL is where the browser posts the manifest: the organisation's
// "new App" page, or the user's when no organisation is named.
func ManifestFormURL(githubOrg, state string) string {
	u := "https://github.com/settings/apps/new"
	if o := strings.TrimSpace(githubOrg); o != "" {
		u = "https://github.com/organizations/" + url.PathEscape(o) + "/settings/apps/new"
	}
	return u + "?state=" + url.QueryEscape(state)
}

// InstallURL is where an organisation installs an App and picks repositories.
func InstallURL(slug, state string) string {
	return "https://github.com/apps/" + url.PathEscape(slug) + "/installations/new?state=" + url.QueryEscape(state)
}
