package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/secrets"
	"covey/internal/target/manifestplug"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Einrichtung eines Zielsystems — der Zustand, den der Assistent führt.
//
// Bisher stand die Einrichtung als Fließtext in SetupDoc, und jeder Schritt
// darin führte woandershin: Secrets auf die eine Seite, ACCESS.md in den
// Config-Editor des Agenten, die Webhook-URL musste man sich aus der
// öffentlichen Adresse und einem Agenten-Slug selbst zusammensetzen. Ob es am
// Ende zusammenpasste, zeigte sich beim ersten Lauf.
//
// Dieser Endpunkt beantwortet dieselben Fragen als Zustand statt als Prosa:
// Welche Zugangsdaten braucht das Plugin und liegen sie? Welche Scopes kennt
// es? Nimmt es Webhooks an, und wie lautet die Adresse dann konkret? Welcher
// Agent hat es schon in seiner ACCESS.md? Der Prosa-Teil bleibt — aber nur für
// das, was im FREMDEN System zu tun ist, denn das kann diese Oberfläche nicht
// für jemanden erledigen.

type setupCredential struct {
	Key  string `json:"key"`
	Kind string `json:"kind"` // "url" | "token"
	// Stored: ein Wert liegt org-weit vor. Nicht der Wert selbst — der ist
	// write-only und bleibt es auch für einen Assistenten.
	Stored bool `json:"stored"`
	// Optional: das Plugin arbeitet auch ohne diesen Wert.
	Optional bool `json:"optional"`
}

type setupWebhook struct {
	Supported bool `json:"supported"`
	// URL mit der echten öffentlichen Adresse; <agent-slug> bleibt als
	// Platzhalter stehen, bis der Assistent den Agenten kennt.
	URL string `json:"url,omitempty"`
	// SecretEnv ist die Prozess-Variable mit dem HMAC-Geheimnis, SecretSet
	// sagt, ob sie gesetzt ist. Ohne sie nimmt der Endpunkt zwar an, aber
	// ungeprüft — und das sollte man sehen, bevor man es produktiv nutzt.
	SecretEnv string `json:"secret_env,omitempty"`
	SecretSet bool   `json:"secret_set"`
}

type setupAgent struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	// Access: der Agent hat das System in seiner ACCESS.md.
	Access bool     `json:"access"`
	Scopes []string `json:"scopes,omitempty"`
}

type setupState struct {
	Name        string            `json:"name"`
	Label       string            `json:"label"`
	Enabled     bool              `json:"enabled"`
	Credentials []setupCredential `json:"credentials"`
	Scopes      []string          `json:"scopes,omitempty"`
	Webhook     setupWebhook      `json:"webhook"`
	// Probe: das Plugin kann eine Verbindung prüfen. Wo es das nicht kann,
	// überspringt der Assistent den Schritt sichtbar, statt ein Häkchen zu
	// setzen, für das er keinen Beleg hat.
	Probe    bool         `json:"probe"`
	Agents   []setupAgent `json:"agents"`
	SetupDoc string       `json:"setup_doc,omitempty"`
}

func (s *Server) handleTargetSetup(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")

	list, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	var found bool
	var state setupState
	var kind string
	var definition json.RawMessage
	for _, pl := range list {
		if pl.Name != name {
			continue
		}
		found = true
		kind, definition = pl.Kind, pl.Manifest
		state = setupState{
			Name:     pl.Name,
			Label:    pl.Label,
			Enabled:  pl.Enabled,
			SetupDoc: strings.ReplaceAll(pl.SetupDoc, "{public_url}", s.origin(r)),
		}
	}
	if !found {
		writeErr(w, http.StatusNotFound, "unknown target system")
		return
	}
	// Die Flags (welche Zugangsdaten, welche Scopes) stehen im Descriptor des
	// kompilierten Plugins. Manifest- und MCP-Plugins haben keinen — dort
	// bleiben die Felder leer, und der Assistent zeigt entsprechend weniger.
	plugin, _ := target.Describe(name)
	state.Scopes = plugin.Scopes
	// Ein Manifest-Plugin hat keinen Descriptor, aber sein Scope-Vokabular
	// steht in der Datei — sonst böte der Assistent für Katalog-Plugins gar
	// keine Scopes an und jedes Wort in ACCESS.md wäre geraten.
	if len(state.Scopes) == 0 && kind == "custom" {
		if m, err := manifestplug.Parse(definition); err == nil {
			state.Scopes = m.Scopes
		}
	}

	// Welche Zugangsdaten das Plugin braucht, steht in seinen Flags — die
	// Namenskonvention <system>_url/<system>_token ist dieselbe, die der
	// Broker zur Laufzeit auflöst.
	if !plugin.NoCredentials {
		keys, err := s.Secrets.Keys(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		has := map[string]bool{}
		for _, k := range keys {
			has[k] = true
		}
		if !plugin.BaseURLOptional {
			state.Credentials = append(state.Credentials, setupCredential{
				Key: name + "_url", Kind: "url", Stored: has[name+"_url"],
			})
		} else {
			state.Credentials = append(state.Credentials, setupCredential{
				Key: name + "_url", Kind: "url", Stored: has[name+"_url"], Optional: true,
			})
		}
		state.Credentials = append(state.Credentials, setupCredential{
			Key: name + "_token", Kind: "token", Stored: has[name+"_token"],
			Optional: plugin.CredentialsOptional,
		})
	}

	// Webhook: Ob ein Plugin welche annimmt, steht nicht in einer Liste,
	// sondern in dem, was es implementiert (target.Webhooker). Deshalb wird
	// hier gefragt und nicht nachgeschlagen.
	// Definition statt target.Get: ein Manifest-Plugin steht nicht in der
	// kompilierten Registry, kann aber genauso einen Webhook annehmen und seine
	// Verbindung testen — es sagt das nur in seiner Datei statt in seinem
	// Methodensatz. target.Probes fragt beides zusammen.
	if sys, err := s.Targets.Definition(r.Context(), p.OrgID, name); err == nil {
		if _, isHook := sys.(target.Webhooker); isHook {
			env := "COVEY_" + strings.ToUpper(name) + "_WEBHOOK_SECRET"
			state.Webhook = setupWebhook{
				Supported: true,
				URL:       s.origin(r) + "/api/webhooks/" + name + "/<agent-slug>",
				SecretEnv: env,
				SecretSet: s.WebhookSecrets[name] != "",
			}
		}
		_, state.Probe = target.Probes(sys)
	}

	// Wer hat das System schon? Die Antwort steht in der ACCESS.md der
	// Agenten, also dort, wo sie auch gilt.
	agentList, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for _, a := range agentList {
		entry := setupAgent{ID: a.ID, Slug: a.Slug, DisplayName: a.DisplayName}
		if cfg, err := s.Registry.CurrentConfig(r.Context(), a.ID); err == nil {
			for _, acc := range agents.ParseAccess(cfg.Files["ACCESS.md"]) {
				if acc.System == name {
					entry.Access = true
					entry.Scopes = acc.Scopes
					break
				}
			}
		}
		state.Agents = append(state.Agents, entry)
	}
	// Leere Listen sind leere Listen und nicht null. Das Feld heisst
	// `credentials` ohne omitempty, die Oberflaeche liest es als Array — ein
	// nil-Slice wird in JSON aber zu null, und `null.length` beendet die
	// Einrichtung mit einem TypeError, bevor sie etwas anzeigt. Getroffen hat
	// es genau die Systeme, die keine Zugangsdaten brauchen (browser, dev):
	// dort haengt an der Fallunterscheidung oben nie ein append.
	if state.Credentials == nil {
		state.Credentials = []setupCredential{}
	}
	if state.Agents == nil {
		state.Agents = []setupAgent{}
	}

	writeJSON(w, http.StatusOK, state)
}

// handleTargetProbe stellt die eine Frage, die "gespeichert" von "funktioniert"
// unterscheidet: Antwortet das System auf die hinterlegten Zugangsdaten, und
// als wen.
//
// Der Aufruf ist lesend und läuft in der Control Plane — das Token verlässt
// sie dabei nicht Richtung Sandbox.
type probeResult struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"`
	Error    string `json:"error,omitempty"`
	// What the plugin knows about the credential's life, where it does
	// (target.CredentialInspector): the date it runs out, and whether covey
	// can renew it itself.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Rotatable bool       `json:"rotatable,omitempty"`
}

func (s *Server) handleTargetProbe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")

	sys, err := s.Targets.Definition(r.Context(), p.OrgID, name)
	if err != nil {
		writeErr(w, http.StatusNotFound, "unknown target system")
		return
	}
	inspector, inspects := target.Inspects(sys)
	prober, probes := target.Probes(sys)
	if !inspects && !probes {
		writeErr(w, http.StatusBadRequest, "this target system cannot test its connection")
		return
	}

	d, _ := target.Describe(name)
	var cred target.Credential
	if !d.NoCredentials {
		token, err := s.orgSecret(r.Context(), p.OrgID, name+"_token")
		if err != nil {
			writeJSON(w, http.StatusOK, probeResult{Error: "no " + name + "_token stored"})
			return
		}
		base, err := s.orgSecret(r.Context(), p.OrgID, name+"_url")
		if err != nil && !d.BaseURLOptional {
			writeJSON(w, http.StatusOK, probeResult{Error: "no " + name + "_url stored"})
			return
		}
		// The connection test uses the same trust anchor the runs will: a
		// probe that trusts more than the action does would report a health
		// the agent never gets to see.
		ca, _ := s.orgSecret(r.Context(), p.OrgID, name+"_ca")
		cred = target.Credential{BaseURL: base, Token: token, CA: ca}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var info target.CredentialInfo
	if inspects {
		info, err = inspector.Inspect(ctx, cred)
	} else {
		info.Identity, err = prober.Probe(ctx, cred)
	}
	// What the test saw is kept beside the token — the same columns the
	// daily check writes, so the wizard's button and the loop tell one
	// story. A system without credentials has no row to write to.
	if !d.NoCredentials {
		rec := secrets.Probe{At: time.Now(), Identity: info.Identity, ExpiresAt: info.ExpiresAt,
			CredentialID: info.ID, Rotatable: info.Rotatable}
		if err != nil {
			rec.Err, rec.Rejected = err.Error(), daemon.CredentialRejected(err)
		}
		_ = s.Secrets.RecordProbe(r.Context(), secrets.Ref{OrgID: p.OrgID, Key: name + "_token"}, rec)
	}
	if err != nil {
		// Die Fehlermeldung des Zielsystems steht hier bewusst so, wie sie
		// kam: "HTTP 401" ist für den, der gerade einen Token eingesetzt hat,
		// die brauchbarste Auskunft, die es gibt.
		writeJSON(w, http.StatusOK, probeResult{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, probeResult{OK: true, Identity: info.Identity, ExpiresAt: info.ExpiresAt, Rotatable: info.Rotatable})
}

// orgSecret liest einen org-weiten Wert (Slot 0) — den, den der Broker zur
// Laufzeit auch nähme.
func (s *Server) orgSecret(ctx context.Context, orgID uuid.UUID, key string) (string, error) {
	return s.Secrets.Value(ctx, orgID, key, 0)
}
