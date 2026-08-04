package nextcloud

import (
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

// System bindet Nextcloud-Dateien als Zielsystem-Plugin an die target-
// Registry: eine per Freigabelink (oder Konto-Zugang) bereitgestellte
// Dateiablage, in der der Agent Dateien listet, liest, schreibt, in die
// Sandbox lädt und ablegt. Es gibt keinen Webhook-Eingang — der Intake läuft
// bei Bedarf per HEARTBEAT.md-Polling wie beim SharePoint-/E-Mail-Plugin.
type System struct{}

func init() {
	target.Register(target.Descriptor{
		Name:        "nextcloud",
		Label:       "Nextcloud-Dateien",
		Description: "Eine Nextcloud-Dateiablage per Freigabelink oder Konto-Zugang: Dateien listen (list), lesen (read/download in die Sandbox), ablegen und bearbeiten (write/upload), Ordner anlegen (mkdir), löschen (delete). Zugriff über WebDAV; einem Bot genügt der Link. Secrets nextcloud_url (Freigabelink https://host/s/… ODER Server-URL) + nextcloud_token (Share-Passwort bzw. benutzer:app-passwort). Intake per HEARTBEAT.md (Polling, kein Webhook).",
		Kind:        "builtin",
		Category:    target.CategoryFiles,
		System:      System{},
		SetupDoc: `Zwei Wege — der einfachste zuerst:

A) Öffentlicher Freigabelink (empfohlen, „Bot einen Link schicken"):
   1. In Nextcloud den Ordner öffnen, den der Agent bearbeiten soll →
      Teilen → „Link teilen". Als Berechtigung „Bearbeiten erlauben"
      wählen (sonst kann der Agent nur lesen). Dringend empfohlen: ein
      Passwort auf die Freigabe setzen.
   2. Den Freigabelink kopieren (Form https://cloud.example.com/s/AbCdEf).
   3. Unter Secrets hinterlegen und dem Agenten zuweisen:
      nextcloud_url   = der Freigabelink aus Schritt 2
      nextcloud_token = das Share-Passwort  (oder "-" wenn die Freigabe
                        kein Passwort hat)

B) Konto-Zugang (das ganze Datei-Verzeichnis eines Nutzers):
   1. In Nextcloud → Einstellungen → Sicherheit → „App-Passwort erzeugen"
      (kein Login-Passwort in Covey hinterlegen).
   2. Unter Secrets hinterlegen und dem Agenten zuweisen:
      nextcloud_url   = https://cloud.example.com   (die Server-Basis-URL)
      nextcloud_token = benutzer:app-passwort

3. In der ACCESS.md des Agenten freischalten:
   - system: nextcloud scope: read,write

4. Egress: der Nextcloud-Host (z. B. cloud.example.com) muss aus der
   Sandbox erreichbar sein — als Egress-Host der Org hinterlegen.

5. Optionaler Intake per Heartbeat — in der HEARTBEAT.md des Agenten:
   - alle: 30m titel: Ablage sichten aufgabe: Liste mit list den
     Eingangsordner und bearbeite neue Dateien nach Playbook.

Details: docs/ops-nextcloud.md im Repository.`,
	})
}

func (System) Name() string { return "nextcloud" }

// VerifyWebhook/ParseWebhook: kein Webhook-Eingang — der Intake läuft per
// Heartbeat-Polling.
func (System) VerifyWebhook(string, []byte, http.Header) bool { return false }

func (System) ParseWebhook([]byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("nextcloud hat keinen webhook-eingang (intake per heartbeat)")
}

// ActionSubject: jede Aktion ist ihr eigenes Guard-Rail-Subjekt — delete und
// write lassen sich so schärfer regeln als das reine Lesen.
func (System) ActionSubject(action string, _ json.RawMessage) string {
	return "nextcloud:" + action
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
			out["hint"] = fmt.Sprintf("Mehr als %d Einträge — mit path in einen Unterordner eingrenzen.", listMax)
		}
		return out, nil
	},
	"read": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
			return nil, fmt.Errorf("datei %q ist zu gross oder binaer fuer read — mit download in die Sandbox holen", relPath)
		}
		return map[string]any{"path": relPath, "size": len(data), "content": string(data)}, nil
	},
	"write": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
		data, err := io.ReadAll(f)
		if err != nil {
			return nil, err
		}
		return c.Upload(ctx, to, data)
	},
	"download": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
			"hint": "Datei liegt lokal — direkt lesen/bearbeiten und danach mit upload zurücklegen."}, nil
	},
	"mkdir": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		return c.Mkdir(ctx, relPath)
	},
	"delete": func(ctx context.Context, c *Client, relPath string, in aktionsParams) (any, error) {
		if relPath == "" {
			return nil, fmt.Errorf("path fehlt")
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
		return nil, fmt.Errorf("unbekannte aktion %q", strings.TrimSpace(action))
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

// localPath löst einen vom Agenten angegebenen Sandbox-Pfad sicher gegen das
// Arbeitsverzeichnis auf: relativ zum Workdir, kein Ausbruch per ".." oder
// absolutem Pfad außerhalb.
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
