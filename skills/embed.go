// Package skills embeds the bundled Claude Code skills into the binary, so a
// running covey instance can offer them for download, including to users
// without Git access. The files here under skills/<name>/ are the source of
// truth; `make build` syncs the copy under .claude/skills/<name>/ (for Claude
// Code inside the repo itself) from them.
package skills

import "embed"

//go:embed covey-agent
var FS embed.FS
