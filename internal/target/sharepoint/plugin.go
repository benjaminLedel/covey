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

// System bindet SharePoint/Teams-Dateien als Zielsystem-Plugin an die
// target-Registry: eine per Freigabelink bereitgestellte Dokumentbibliothek,
// in der der Agent Dateien listet, liest, schreibt, in die Sandbox lädt und
// ablegt. Es gibt keinen Webhook-Eingang — Graph-Change-Notifications
// brauchen eine öffentlich validierte HTTPS-Subscription; der Intake läuft
// bei Bedarf per HEARTBEAT.md-Polling wie beim E-Mail-Plugin.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "sharepoint",
		Label:       "SharePoint / Teams-Dateien",
		Description: "Eine SharePoint-/Teams-Dokumentbibliothek per Freigabelink: Dateien listen (list), lesen (read/download in die Sandbox), ablegen und bearbeiten (write/upload), Ordner anlegen (mkdir), löschen (delete). Auth über eine Entra-ID-App-Registrierung (Client-Credentials), Secrets sharepoint_url (Freigabelink) + sharepoint_token (tenant:client:secret). Intake per HEARTBEAT.md (Polling, kein Webhook).",
		Kind:        "builtin",
		Category:    target.CategoryFiles,
		System:      System{},
		SetupDoc: `1. In Entra ID (Azure-Portal → App-Registrierungen) eine App für den
   Agenten registrieren. Unter API-Berechtigungen die Anwendungs-
   berechtigung (Application) Microsoft Graph → Files.ReadWrite.All
   hinzufügen und Admin-Zustimmung erteilen. Least-Privilege-Alternative:
   Sites.Selected und der App per Graph gezielt Zugriff auf die eine
   Site geben. Dann ein Client-Secret erzeugen (Zertifikate & Geheimnisse).

2. In SharePoint oder Teams den Ordner bzw. die Dokumentbibliothek
   öffnen, in der der Agent arbeiten soll, und den Link kopieren
   (SharePoint: „Link kopieren"; Teams: Dateien-Tab → „Link kopieren").
   Der Link muss auf einen Ordner zeigen, nicht auf eine einzelne Datei.

3. Unter Secrets hinterlegen und dem Agenten zuweisen:
   sharepoint_url   = der Freigabelink aus Schritt 2
   sharepoint_token = tenant-id:client-id:client-secret

4. In der ACCESS.md des Agenten freischalten:
   - system: sharepoint scope: read,write

5. Egress: graph.microsoft.com und login.microsoftonline.com müssen aus
   der Sandbox erreichbar sein.

6. Optionaler Intake per Heartbeat — in der HEARTBEAT.md des Agenten:
   - alle: 30m titel: Ablage sichten aufgabe: Liste mit list den
     Eingangsordner und bearbeite neue Dateien nach Playbook.

Details: docs/ops-sharepoint.md im Repository.`,
	})
}

func (System) Name() string { return "sharepoint" }

// VerifyWebhook/ParseWebhook: kein Webhook-Eingang — Graph-Subscriptions
// (Change Notifications) brauchen eine öffentlich erreichbare, validierte
// HTTPS-URL; der Intake läuft per Heartbeat-Polling.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("sharepoint hat keinen webhook-eingang (intake per heartbeat)")
}

// ActionSubject: jede Aktion ist ihr eigenes Guard-Rail-Subjekt — delete
// und write lassen sich so schärfer regeln als das reine Lesen.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "sharepoint:" + action
}

// aktionsParams ist die Vereinigung aller Parameter, die irgendeine Aktion
// dieses Zielsystems braucht — der Agent schickt ein flaches JSON-Objekt,
// was darin fehlt, bleibt leer.
type aktionsParams struct {
	Path    string `json:"path"`
	To      string `json:"to"`
	From    string `json:"from"`
	Content string `json:"content"`
}

// aktion fuehrt EINE Aktion aus. Frueher war jede ein Fall in einem langen
// switch; jetzt ist sie fuer sich lesbar und die Verteilung eine Tabelle.
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
			out["hint"] = "Mehr als 200 Einträge — mit path in Unterordner eingrenzen."
		}
		return out, nil
	},
	"read": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
			return nil, fmt.Errorf("datei %q ist zu gross oder binaer fuer read — mit download in die Sandbox holen", relPath)
		}
		return map[string]any{"path": relPath, "size": len(data), "content": string(data)}, nil
	},
	"write": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
			return nil, fmt.Errorf("lokale datei: %w", err)
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			return nil, fmt.Errorf("%q ist ein Verzeichnis — upload überträgt einzelne Dateien", in.From)
		}
		if st.Size() > uploadMaxBytes() {
			return nil, fmt.Errorf("datei %q ist zu gross (%d Bytes, Limit %d)", in.From, st.Size(), uploadMaxBytes())
		}
		to, err := cleanRemotePath(in.To)
		if err != nil {
			return nil, err
		}
		if to == "" {
			to = filepath.Base(local)
		}
		// Vollständig einlesen, damit der PUT eine Content-Length trägt —
		// Graph mag keine chunked Simple-Uploads. Das Limit ist geprüft.
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return c.Upload(ctx, root, to, bytes.NewReader(data))
	},
	"download": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
			"hint": "Datei liegt lokal — direkt lesen/bearbeiten und danach mit upload zurücklegen."}, nil
	},
	"mkdir": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		return c.Mkdir(ctx, root, relPath)
	},
	"delete": func(ctx context.Context, c *Client, root Root, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
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

// localPath löst einen vom Agenten angegebenen Sandbox-Pfad sicher gegen
// das Arbeitsverzeichnis auf: relativ zum Workdir, kein Ausbruch per ".."
// oder absolutem Pfad außerhalb.
func localPath(ctx context.Context, p string) (string, error) {
	workdir := target.Workdir(ctx)
	if workdir == "" {
		return "", fmt.Errorf("keine Sandbox (kein Arbeitsverzeichnis im Kontext)")
	}
	p = strings.TrimSpace(p)
	if p == "" {
		return "", fmt.Errorf("lokaler pfad fehlt")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workdir, p)
	}
	resolved := filepath.Clean(p)
	if resolved != workdir && !strings.HasPrefix(resolved, workdir+string(filepath.Separator)) {
		return "", fmt.Errorf("pfad %q liegt ausserhalb des Sandbox-Arbeitsverzeichnisses", p)
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
