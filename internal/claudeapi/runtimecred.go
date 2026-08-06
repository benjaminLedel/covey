package claudeapi

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"

	"covey/internal/secrets"
)

// Die beiden Staemme, an denen ein Laufzeit-Credential zu erkennen ist.
const (
	// KeyAPIKey ist der Anthropic-API-Key (Konsole, abgerechnet ueber die
	// Organisation) — er meldet sich als x-api-key an.
	KeyAPIKey = "anthropic_api_key"
	// KeyOAuth ist das Abo-Token (`claude setup-token`, laeuft auf das
	// Kontingent eines Menschen) — es meldet sich als Bearer an.
	KeyOAuth = "claude_code_oauth_token"
)

// RuntimePrefixes sind die beiden Staemme fuer SQL, das nur wissen muss, ob
// eine Organisation ueberhaupt ein Laufzeit-Credential haelt (Onboarding).
var RuntimePrefixes = []string{KeyAPIKey, KeyOAuth}

// RuntimeKind ist die Sorte eines Laufzeit-Credentials. Sie entscheidet ueber
// die Anmelde-Mechanik (Header hier, Env-Variable in der Sandbox) — und sie
// steht immer im Namen des Secrets, nie in seinem Wert.
type RuntimeKind int

const (
	KindNone RuntimeKind = iota
	KindAPIKey
	KindOAuth
)

// Classify liest die Sorte aus dem NAMEN eines Secrets. Getroffen ist der Stamm
// selbst oder der Stamm plus '_<Suffix>' — anthropic_api_key_team_a zaehlt,
// anthropic_api_keyring nicht. So haelt eine Organisation beliebig viele
// Credentials nebeneinander, ohne dass ein zufaellig aehnlicher Secret-Name
// stillschweigend zu einem wird.
//
// Bewusst nicht am Wert-Praefix (sk-ant-oat…/sk-ant-api…) geraten: der Name ist
// die gemeinte Bindung. Wer sich vertut, faellt beim Speichern auf
// checkCredential auf, nicht erst im naechsten Lauf.
func Classify(key string) RuntimeKind {
	switch {
	case matchesStem(key, KeyAPIKey):
		return KindAPIKey
	case matchesStem(key, KeyOAuth):
		return KindOAuth
	}
	return KindNone
}

func matchesStem(key, stem string) bool {
	return key == stem || strings.HasPrefix(key, stem+"_")
}

// EnvVar ist die Umgebungsvariable, unter der Claude Code in der Sandbox das
// Credential erwartet. Leer bei KindNone.
func (k RuntimeKind) EnvVar() string {
	switch k {
	case KindAPIKey:
		return "ANTHROPIC_API_KEY"
	case KindOAuth:
		return "CLAUDE_CODE_OAUTH_TOKEN"
	}
	return ""
}

// OAuth sagt, ob sich das Credential als Bearer plus OAuth-Beta-Header anmeldet
// (Abo-Token) statt als x-api-key (API-Key).
func (k RuntimeKind) OAuth() bool { return k == KindOAuth }

// Stem ist der klassische, unsuffigierte Name der Sorte — fuer Hinweistexte in
// der Oberflaeche und in Fehlermeldungen.
func (k RuntimeKind) Stem() string {
	switch k {
	case KindAPIKey:
		return KeyAPIKey
	case KindOAuth:
		return KeyOAuth
	}
	return ""
}

// String ist der Bezeichner der Sorte in der API (JSON).
func (k RuntimeKind) String() string {
	switch k {
	case KindAPIKey:
		return "api_key"
	case KindOAuth:
		return "oauth"
	}
	return ""
}

// Candidate ist ein Secret-Name, der als Laufzeit-Credential in Frage kommt.
// Owned unterscheidet das eigene Secret des Agenten vom organisationsweiten;
// im Organisations-Zusammenhang ist es bedeutungslos.
type Candidate struct {
	Key   string
	Kind  RuntimeKind
	Owned bool
}

// Rank sortiert Kandidaten in fester Ordnung: das eigene Secret des Agenten vor
// dem organisationsweiten, API-Key vor Abo-Token, dann der Name aufsteigend.
// Gleiche Eingabe, gleiches Credential — in jedem Prozess und in jeder Nacht.
// Ohne diese Festlegung entschiede die Reihenfolge der Datenbank darueber,
// welches Konto ein Lauf belastet.
func Rank(c []Candidate) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Owned != c[j].Owned {
			return c[i].Owned
		}
		if c[i].Kind != c[j].Kind {
			return c[i].Kind == KindAPIKey
		}
		return c[i].Key < c[j].Key
	})
}

// Resolved ist das gefundene Credential: Name, Sorte und Wert.
type Resolved struct {
	Key   string
	Kind  RuntimeKind
	Value string
}

// ErrPinnedMissing meldet, dass der festgelegte Schluessel den Agenten nicht
// mehr erreicht — geloescht, oder die Zuweisung wurde zurueckgenommen.
var ErrPinnedMissing = errors.New("festgelegtes Laufzeit-Credential erreicht diesen Agenten nicht")

// ResolveAgent findet das Laufzeit-Credential EINES Agenten.
//
// Ist ein Schluessel festgelegt (agents.runtime_credential_key), zaehlt nur
// dieser: erreicht er den Agenten nicht mehr, ist das ein Fehler und kein
// stiller Rueckfall auf das naechstbeste Token. Der Rueckfall waere schlimmer
// als der Abbruch — er belastet ein fremdes Konto, und genau dagegen ist die
// Festlegung da.
//
// Ohne Festlegung gilt die bisherige Reihenfolge: erst der klassische
// anthropic_api_key, dann das klassische claude_code_oauth_token, dann der Rest
// in der Ordnung aus Rank. Die ersten beiden Schritte halten jede bestehende
// Installation buchstabengleich.
func ResolveAgent(ctx context.Context, store secrets.Store, orgID, agentID uuid.UUID, pinned string) (Resolved, error) {
	if pinned != "" {
		kind := Classify(pinned)
		if kind == KindNone {
			return Resolved{}, ErrPinnedMissing
		}
		v, err := store.Resolve(ctx, orgID, agentID, pinned)
		if err != nil || strings.TrimSpace(v) == "" {
			return Resolved{}, ErrPinnedMissing
		}
		return Resolved{Key: pinned, Kind: kind, Value: strings.TrimSpace(v)}, nil
	}

	for _, classic := range []string{KeyAPIKey, KeyOAuth} {
		if v, err := store.Resolve(ctx, orgID, agentID, classic); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return Resolved{Key: classic, Kind: Classify(classic), Value: v}, nil
			}
		}
	}

	keys, err := store.ResolvableKeys(ctx, orgID, agentID)
	if err != nil {
		return Resolved{}, err
	}
	var cands []Candidate
	for _, k := range keys {
		if k.Key == KeyAPIKey || k.Key == KeyOAuth {
			continue // oben schon versucht
		}
		if kind := Classify(k.Key); kind != KindNone {
			cands = append(cands, Candidate{Key: k.Key, Kind: kind, Owned: k.Owned})
		}
	}
	Rank(cands)
	for _, c := range cands {
		if v, err := store.Resolve(ctx, orgID, agentID, c.Key); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return Resolved{Key: c.Key, Kind: c.Kind, Value: v}, nil
			}
		}
	}
	return Resolved{}, secrets.ErrNotFound
}

// ResolveOrgNamed findet das Credential einer Organisation — ohne Agenten, fuer
// die Arbeit, die die Control Plane selbst erledigt (Config-Copilot). Es liefert
// zusaetzlich den NAMEN, damit die Oberflaeche sagen kann, auf welchem Token
// der Copilot laeuft, statt es geheimnisvoll zu lassen.
//
// Reihenfolge wie bei ResolveAgent, nur ohne die Ebene der agenteneigenen
// Secrets: klassischer API-Key, klassisches Abo-Token, dann der Rest nach Rank.
// API-Key zuerst bleibt richtig — der Copilot ist Automatik des Servers, und
// ein Abo-Token traegt das Kontingent eines einzelnen Menschen.
func ResolveOrgNamed(ctx context.Context, store secrets.Store, orgID uuid.UUID) (key, cred string, oauth, ok bool) {
	for _, classic := range []string{KeyAPIKey, KeyOAuth} {
		if v, err := store.Get(ctx, orgID, classic); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return classic, v, Classify(classic).OAuth(), true
			}
		}
	}
	keys, err := store.Keys(ctx, orgID)
	if err != nil {
		return "", "", false, false
	}
	var cands []Candidate
	for _, k := range keys {
		if k == KeyAPIKey || k == KeyOAuth {
			continue
		}
		if kind := Classify(k); kind != KindNone {
			cands = append(cands, Candidate{Key: k, Kind: kind})
		}
	}
	Rank(cands)
	for _, c := range cands {
		if v, err := store.Get(ctx, orgID, c.Key); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				return c.Key, v, c.Kind.OAuth(), true
			}
		}
	}
	return "", "", false, false
}
