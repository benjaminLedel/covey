//go:build !nopack

package main

// The target-system plugins this build ships with. The blank import is the
// whole mechanism: the package registers itself with the SDK registry at init,
// and the store then offers it for activation rather than for installation
// (spec/22-plugin-marketplace.md).
//
// They sit behind the `nopack` build tag so that a build without them needs no
// edit to the source any more: `go build -tags nopack ./cmd/covey` leaves the
// whole pack out, and such an instance takes its target systems from the
// catalogue. Every compiled plugin is code in everyone's binary, whether the
// system is used or not, and a release they have to wait for when it changes —
// the tag is how an operator who uses none of them stops carrying all of them.
//
// Finer than the tag: delete a single line here. The rest stays as it is.
import (
	_ "github.com/benjaminLedel/covey-plugin-pack/browser"
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
