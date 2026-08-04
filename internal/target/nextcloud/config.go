package nextcloud

import (
	"fmt"
	"net/url"
	"strings"
)

// Configuration of the Nextcloud plugin out of the brokered secret pair
// (nextcloud_url + nextcloud_token). Nextcloud speaks WebDAV; the plugin
// supports two modes, told apart by nextcloud_url alone:
//
//  1. Share link ("sending a bot a link"): the public link of a shared
//     folder, e.g. https://cloud.example.com/s/AbCdEf. WebDAV then runs over
//     /public.php/webdav/, basic auth with the share token as the user and
//     the share password as the password:
//
//     nextcloud_url   = https://cloud.example.com/s/AbCdEf
//     nextcloud_token = <share password>   (or "-" when the share has no
//                       password)
//
//  2. Account login (a user's whole file tree): the server base URL plus
//     user:app-password. WebDAV runs over /remote.php/dav/files/<user>/:
//
//     nextcloud_url   = https://cloud.example.com
//     nextcloud_token = alice:<app password>
//
// In both cases all paths of the actions are relative to the WebDAV root (the
// shared folder resp. the account's file root). Breaking out through ".." is
// rejected on the daemon side.

// Config is the parsed connection configuration.
type Config struct {
	// DavBase is the complete WebDAV collection root including the trailing
	// slash (actions append the relative path to it).
	DavBase string
	// User/Pass are the basic-auth credentials (share token resp. account
	// user). Pass may be empty (a share without a password).
	User string
	Pass string
	// Share reports whether this is a public share (true) or an account login
	// (false) — for diagnostics/output only.
	Share bool
}

// passwordSentinels are marker values for "no password" — the broker insists
// that nextcloud_token be set, but a share without a password has none.
// Instead of an empty secret the operator stores one of these values.
var passwordSentinels = map[string]bool{
	"": true, "-": true, "none": true, "kein": true,
	"anonymous": true, "public": true, "x": true,
}

// ParseConfig breaks the brokered credential down into the WebDAV configuration.
func ParseConfig(rawURL, token string) (Config, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Config{}, fmt.Errorf("nextcloud_url missing — store the share link or the server URL")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Config{}, fmt.Errorf("nextcloud_url: %q is not a valid link", rawURL)
	}
	origin := u.Scheme + "://" + u.Host

	// Mode 1: share link (/s/<token>, optionally preceded by /index.php and
	// an installation subdirectory).
	if prefix, shareToken, ok := parseShareLink(u.Path); ok {
		pass := token
		if passwordSentinels[strings.ToLower(strings.TrimSpace(token))] {
			pass = ""
		}
		return Config{
			DavBase: origin + prefix + "/public.php/webdav/",
			User:    shareToken,
			Pass:    pass,
			Share:   true,
		}, nil
	}

	// Mode 2: account login. nextcloud_token = user:app-password.
	user, pass, found := strings.Cut(token, ":")
	if !found || user == "" || pass == "" {
		return Config{}, fmt.Errorf("nextcloud_token must be %q (account login) — or point nextcloud_url at an /s/ share link", "user:app-password")
	}
	// Cut off a /remote.php/... that may have been supplied, so that we point
	// cleanly at the user's file root. An installation subdirectory (e.g.
	// /nextcloud) is preserved.
	prefix := strings.TrimRight(u.Path, "/")
	if i := strings.Index(prefix, "/remote.php"); i >= 0 {
		prefix = prefix[:i]
	} else if i := strings.Index(prefix, "/index.php"); i >= 0 {
		prefix = prefix[:i]
	}
	return Config{
		DavBase: origin + prefix + "/remote.php/dav/files/" + url.PathEscape(user) + "/",
		User:    user,
		Pass:    pass,
		Share:   false,
	}, nil
}

// parseShareLink recognizes a Nextcloud share link and returns the
// installation prefix (empty or e.g. "/nextcloud") along with the share token.
func parseShareLink(path string) (prefix, token string, ok bool) {
	idx := strings.Index(path, "/s/")
	if idx < 0 {
		return "", "", false
	}
	token = path[idx+len("/s/"):]
	if i := strings.IndexByte(token, '/'); i >= 0 {
		token = token[:i] // cut off a trailing /download or the like
	}
	if token == "" {
		return "", "", false
	}
	prefix = strings.TrimSuffix(path[:idx], "/index.php")
	return prefix, token, true
}
