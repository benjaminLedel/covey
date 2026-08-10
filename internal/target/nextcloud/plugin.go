package nextcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"covey/internal/target"
)

// System binds Nextcloud files in as a target-system plugin to the target
// registry: a file store provided through a share link (or an account login),
// in which the agent lists, reads and writes files, fetches them into the
// sandbox and deposits them. There is no webhook entry — where needed the
// intake runs by HEARTBEAT.md polling, as with the SharePoint/e-mail plugin.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "nextcloud",
		Label:       "Nextcloud files",
		Description: "A Nextcloud file store through a share link or an account login: list files (list), read them (read/download into the sandbox), deposit and edit them (write/upload), create folders (mkdir), delete (delete). Access over WebDAV; a bot needs no more than the link. Secrets nextcloud_url (share link https://host/s/… OR server URL) + nextcloud_token (share password resp. user:app-password). Intake through HEARTBEAT.md (polling, no webhook).",
		Kind:        "builtin",
		Category:    target.CategoryFiles,
		Scopes:      []string{"read", "write"},
		System:      System{},
		SetupDoc: `Two ways — the simplest first:

A) Public share link (recommended, "send the bot a link"):
   1. In Nextcloud open the folder the agent is to work in →
      Share → "Share link". Pick the permission "Allow editing"
      (otherwise the agent can only read). Strongly recommended: put a
      password on the share.
   2. Copy the share link (of the form https://cloud.example.com/s/AbCdEf).
   3. Store under Secrets and assign to the agent:
      nextcloud_url   = the share link from step 2
      nextcloud_token = the share password  (or "-" when the share has no
                        password)

B) Account login (a user's whole file tree):
   1. In Nextcloud → Settings → Security → "Create new app password"
      (do not store a login password in Covey).
   2. Store under Secrets and assign to the agent:
      nextcloud_url   = https://cloud.example.com   (the server base URL)
      nextcloud_token = user:app-password

3. Enable in the agent's ACCESS.md:
   - system: nextcloud scope: read,write

4. Egress: the Nextcloud host (e.g. cloud.example.com) must be reachable
   from the sandbox — store it as an egress host of the org.

5. Optional intake by heartbeat — in the agent's HEARTBEAT.md:
   - alle: 30m titel: Review the file store aufgabe: List the inbox folder
     with list and work on new files according to the playbook.

Details: docs/ops-nextcloud.md in the repository.`,
	})
}

func (System) Name() string { return "nextcloud" }

// Kein target.Webhooker: der Eingang laeuft ueber Heartbeat-Polling. Die
// Schnittstelle bleibt weg statt ablehnend erfuellt — sonst meldet das Plugin
// eine Faehigkeit an, die es verweigert.

// ActionSubject: every action is its own guard-rail subject — that way delete
// and write can be ruled on more sharply than plain reading.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "nextcloud:" + action
}

// aktionsParams is the union of all parameters any action of this target
// system needs — the agent sends a flat JSON object, and whatever is missing
// from it stays empty.
type aktionsParams struct {
	Path    string `json:"path"`
	To      string `json:"to"`
	From    string `json:"from"`
	Content string `json:"content"`
}

// aktion runs ONE action. Each used to be a case in a long switch; now it is
// readable on its own and the dispatch is a table.
type aktion func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error)

var aktionen = map[string]aktion{
	"list": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		entries, rootName, truncated, err := c.List(ctx, relPath)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"path": relPath, "entries": entries}
		if rootName != "" {
			out["root"] = rootName
		}
		if truncated {
			out["truncated"] = true
			out["hint"] = fmt.Sprintf("More than %d entries — narrow this down to a subfolder with path.", listMax)
		}
		return out, nil
	},
	"read": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		body, err := c.Download(ctx, relPath)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		data, err := io.ReadAll(io.LimitReader(body, readMaxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > readMaxBytes || !utf8.Valid(data) {
			return nil, fmt.Errorf("file %q is too large or binary for read — fetch it into the sandbox with download", relPath)
		}
		return map[string]any{"path": relPath, "size": len(data), "content": string(data)}, nil
	},
	"write": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		return c.Upload(ctx, relPath, []byte(in.Content))
	},
	"upload": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		local, err := localPath(ctx, in.From)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(local)
		if err != nil {
			return nil, fmt.Errorf("local file: %w", err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			return nil, fmt.Errorf("%q is a directory — upload transfers single files", in.From)
		}
		if st.Size() > uploadMaxBytes() {
			return nil, fmt.Errorf("file %q is too large (%d bytes, limit %d)", in.From, st.Size(), uploadMaxBytes())
		}
		to, err := cleanRemotePath(in.To)
		if err != nil {
			return nil, err
		}
		if to == "" {
			to = filepath.Base(local)
		}
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return c.Upload(ctx, to, data)
	},
	"download": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		dest := in.To
		if dest == "" {
			dest = filepath.Join("nextcloud", filepath.FromSlash(relPath))
		}
		local, err := localPath(ctx, dest)
		if err != nil {
			return nil, err
		}
		body, err := c.Download(ctx, relPath)
		if err != nil {
			return nil, err
		}
		defer body.Close()
		if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
			return nil, err
		}
		f, err := os.Create(local)
		if err != nil {
			return nil, err
		}
		n, err := io.Copy(f, body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": local, "size": n,
			"hint": "The file is local now — read/edit it directly and put it back with upload afterwards."}, nil
	},
	"mkdir": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		return c.Mkdir(ctx, relPath)
	},
	"delete": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		if err := c.Delete(ctx, relPath); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": relPath}, nil
	},
}

func (System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	fn, ok := aktionen[action]
	if !ok {
		return nil, fmt.Errorf("unknown action %q", strings.TrimSpace(action))
	}
	cfg, err := ParseConfig(cred.BaseURL, cred.Token)
	if err != nil {
		return nil, err
	}
	c := NewClient(cfg)

	var in aktionsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	relPath, err := cleanRemotePath(in.Path)
	if err != nil {
		return nil, err
	}

	return fn(ctx, c, relPath, in)
}

// localPath resolves a sandbox path given by the agent safely against the
// working directory: relative to the workdir, with no breaking out through
// ".." or an absolute path outside it.
func localPath(ctx context.Context, p string) (string, error) {
	workdir := target.Workdir(ctx)
	if workdir == "" {
		return "", fmt.Errorf("no sandbox (no working directory in the context)")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("local path missing")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q lies outside the sandbox working directory", p)
	}
	return resolved, nil
}

func (System) PromptDoc() string {
	return `Available Nextcloud file actions (a file store shared with you; all paths relative to its
   root): list {"path":"subfolder (optional, empty = the root)"} lists files and folders,
   read {"path":"a/b.txt"} returns the content of a text file directly (up to 1 MB, text only),
   write {"path":"a/b.txt","content":"..."} creates a text file or overwrites it (missing
   intermediate folders are created),
   download {"path":"a/report.pdf","to":"local/path (optional)"} fetches a file into your sandbox
   (default: nextcloud/<path>) and returns the local path,
   upload {"from":"local/path","to":"remote/path (optional, default: the file name in the root)"} deposits a
   file from your sandbox (replacing what is there),
   mkdir {"path":"a/b/c"} creates folders (including missing intermediate ones),
   delete {"path":"a/old.txt"} deletes a file or a folder.
   How to work: for binary and Office files (docx, xlsx, pdf, …) ALWAYS download → edit locally →
   upload onto the same remote path; read/write are only for plain text files (md, txt, csv, json).
   upload overwrites without asking — check with list whether the target path is already taken if you do
   not deliberately want to replace it. Delete nothing you did not create yourself without an explicit
   assignment.
   WAITING for new files: Nextcloud has no webhook here — do NOT use the blocked status; end your
   run regularly with done, the next heartbeat run reviews the store again.`
}
