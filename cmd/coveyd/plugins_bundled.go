//go:build !nopack

package main

// The daemon's side of the same list (see cmd/covey/plugins_bundled.go): the
// sandbox has to be able to execute what the control plane brokered. The
// `nopack` tag leaves them out here too — manifest and wasm plugins arrive at
// runtime over the protocol and are unaffected by it.
import (
	_ "github.com/benjaminLedel/covey-plugin-pack/browser"
	_ "github.com/benjaminLedel/covey-plugin-pack/confluence"
	_ "github.com/benjaminLedel/covey-plugin-pack/dev"
	_ "github.com/benjaminLedel/covey-plugin-pack/email"
	_ "github.com/benjaminLedel/covey-plugin-pack/github"
	_ "github.com/benjaminLedel/covey-plugin-pack/gitlab"
	_ "github.com/benjaminLedel/covey-plugin-pack/jira"
	_ "github.com/benjaminLedel/covey-plugin-pack/nextcloud"
	_ "github.com/benjaminLedel/covey-plugin-pack/salesforce"
	_ "github.com/benjaminLedel/covey-plugin-pack/sharepoint"
	_ "github.com/benjaminLedel/covey-plugin-pack/teams"
)
