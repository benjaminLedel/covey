package main

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/config"
	"covey/internal/githubapp"
	"covey/internal/secrets/sealbox"
)

// newGitHubAppService wires GitHub Apps. Own Apps (manifest flow) are always
// possible; the shared App only when COVEY_GITHUB_APP_* is complete. A key that
// is set but unreadable stops the start — silently running without the App an
// operator configured would surface much later as "connect does not work".
func newGitHubAppService(c config.GitHubAppConfig, pool *pgxpool.Pool, box *sealbox.Box, log *slog.Logger) (*githubapp.Service, error) {
	svc := &githubapp.Service{Pool: pool, Box: box, Client: githubapp.NewClient()}
	if c.ID == 0 && c.PrivateKey == "" {
		return svc, nil
	}
	if !c.Enabled() {
		log.Warn("COVEY_GITHUB_APP_* incomplete — the shared GitHub App is not offered (needs ID, SLUG, PRIVATE_KEY, CLIENT_ID, CLIENT_SECRET)")
		return svc, nil
	}
	key, err := githubapp.ParseKey(c.PrivateKey)
	if err != nil {
		return nil, err
	}
	svc.Shared = &githubapp.SharedApp{
		App:           githubapp.App{ID: c.ID, Slug: c.Slug, PrivateKey: key},
		WebhookSecret: c.WebhookSecret,
		ClientID:      c.ClientID,
		ClientSecret:  c.ClientSecret,
	}
	if c.WebhookSecret == "" {
		log.Warn("COVEY_GITHUB_APP_WEBHOOK_SECRET empty — webhooks of the shared GitHub App are refused; agents poll instead")
	}
	log.Info("shared GitHub App offered", "slug", c.Slug, "app_id", c.ID)
	return svc, nil
}
