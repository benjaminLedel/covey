package sharepoint

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"covey/internal/target"
)

// System binds SharePoint/Teams files in as a target-system plugin to the
// target registry: a document library provided through a share link, in which
// the agent lists, reads and writes files, fetches them into the sandbox and
// deposits them. There is no webhook entry — Graph change notifications need a
// publicly validated HTTPS subscription; where needed the intake runs by
// HEARTBEAT.md polling, as with the e-mail plugin.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "sharepoint",
		Label:       "SharePoint / Teams files",
		Description: "A SharePoint/Teams document library through a share link: list files (list), read them (read/download into the sandbox), deposit and edit them (write/upload), create folders (mkdir), delete (delete). Auth through an Entra ID app registration (client credentials), secrets sharepoint_url (share link) + sharepoint_token (tenant:client:secret). Intake through HEARTBEAT.md (polling, no webhook).",
		Kind:        "builtin",
		Category:    target.CategoryFiles,
		Scopes:      []string{"read", "write"},
		System:      System{},
		SetupDoc: `1. In Entra ID (Azure portal → App registrations) register an app for the
   agent. Under API permissions add the application permission
   Microsoft Graph → Files.ReadWrite.All and grant admin consent.
   Least-privilege alternative: Sites.Selected, and give the app targeted
   access to the one site through Graph. Then create a client secret
   (Certificates & secrets).

2. In SharePoint or Teams open the folder resp. the document library the
   agent is to work in and copy the link (SharePoint: "Copy link";
   Teams: files tab → "Copy link").
   The link must point at a folder, not at a single file.

3. Store under Secrets and assign to the agent:
   sharepoint_url   = the share link from step 2
   sharepoint_token = tenant-id:client-id:client-secret

4. Enable in the agent's ACCESS.md:
   - system: sharepoint scope: read,write

5. Egress: graph.microsoft.com and login.microsoftonline.com must be
   reachable from the sandbox.

6. Optional intake by heartbeat — in the agent's HEARTBEAT.md:
   - alle: 30m titel: Review the file store aufgabe: List the inbox folder
     with list and work on new files according to the playbook.

Details: docs/ops-sharepoint.md in the repository.`,
	})
}

func (System) Name() string { return "sharepoint" }

// VerifyWebhook/ParseWebhook: no webhook entry — Graph subscriptions (change
// notifications) need a publicly reachable, validated HTTPS URL; the intake
// runs by heartbeat polling.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("sharepoint has no webhook entry (intake by heartbeat)")
}

// ActionSubject: every action is its own guard-rail subject — that way delete
// and write can be ruled on more sharply than plain reading.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "sharepoint:" + action
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
type aktion func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error)

var aktionen = map[string]aktion{
	"list": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		entries, truncated, err := c.List(ctx, root, relPath)
		if err != nil {
			return nil, err
		}
		out := map[string]any{"root": root.Name, "path": relPath, "entries": entries}
		if truncated {
			out["truncated"] = true
			out["hint"] = "More than 200 entries — narrow this down to subfolders with path."
		}
		return out, nil
	},
	"read": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		body, err := c.Download(ctx, root, relPath)
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
	"write": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		return c.Upload(ctx, root, relPath, strings.NewReader(in.Content))
	},
	"upload": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
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
		// Read it in fully so that the PUT carries a Content-Length — Graph
		// does not like chunked simple uploads. The limit is checked.
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return c.Upload(ctx, root, to, bytes.NewReader(data))
	},
	"download": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		dest := in.To
		if dest == "" {
			dest = filepath.Join("sharepoint", filepath.FromSlash(relPath))
		}
		local, err := localPath(ctx, dest)
		if err != nil {
			return nil, err
		}
		body, err := c.Download(ctx, root, relPath)
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
	"mkdir": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		return c.Mkdir(ctx, root, relPath)
	},
	"delete": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path missing")
		}
		if err := c.Delete(ctx, root, relPath); err != nil {
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
	tok, err := cfg.AccessToken(ctx)
	if err != nil {
		return nil, err
	}
	c := NewClient(cfg.GraphBase, tok)
	root, err := c.ResolveRoot(ctx, cfg.ShareLink)
	if err != nil {
		return nil, err
	}

	var in aktionsParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, fmt.Errorf("params: %w", err)
	}
	relPath, err := cleanRemotePath(in.Path)
	if err != nil {
		return nil, err
	}

	return fn(ctx, c, root, relPath, in)
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
	return `Available SharePoint/Teams file actions (a document library shared with you; all paths
   relative to its root): list {"path":"subfolder (optional, empty = the root)"} lists files and folders,
   read {"path":"a/b.txt"} returns the content of a text file directly (up to 1 MB, text only),
   write {"path":"a/b.txt","content":"..."} creates a text file or overwrites it,
   download {"path":"a/report.docx","to":"local/path (optional)"} fetches a file into your sandbox
   (default: sharepoint/<path>) and returns the local path,
   upload {"from":"local/path","to":"remote/path (optional, default: the file name in the root)"} deposits a
   file from your sandbox into the library (replacing what is there),
   mkdir {"path":"a/b/c"} creates folders (including missing intermediate ones),
   delete {"path":"a/old.txt"} deletes a file or a folder.
   How to work: for binary and Office files (docx, xlsx, pdf, …) ALWAYS download → edit locally →
   upload onto the same remote path; read/write are only for plain text files (md, txt, csv, json).
   upload overwrites without asking — check with list whether the target path is already taken if you do
   not deliberately want to replace it. Delete nothing you did not create yourself without an explicit
   assignment.
   WAITING for new files: SharePoint has no webhook here — do NOT use the blocked status; end your
   run regularly with done, the next heartbeat run reviews the store again.`
}
