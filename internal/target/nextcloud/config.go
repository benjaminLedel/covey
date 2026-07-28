package nextcloud

import (
	"fmt"
	"net/url"
	"strings"
)

// Konfiguration des Nextcloud-Plugins aus dem gebrokerten Secret-Paar
// (nextcloud_url + nextcloud_token). Nextcloud spricht WebDAV; das Plugin
// unterstützt zwei Betriebsarten, allein am nextcloud_url erkannt:
//
//  1. Freigabelink („einem Bot einen Link schicken"): der öffentliche Link
//     eines geteilten Ordners, z. B. https://cloud.example.com/s/AbCdEf.
//     WebDAV läuft dann über /public.php/webdav/, Basic-Auth mit dem Share-
//     Token als Benutzer und dem Share-Passwort als Passwort:
//
//     nextcloud_url   = https://cloud.example.com/s/AbCdEf
//     nextcloud_token = <Share-Passwort>   (oder "-" wenn die Freigabe
//                       kein Passwort hat)
//
//  2. Konto-Zugang (das ganze Dateiverzeichnis eines Nutzers): die
//     Server-Basis-URL plus Benutzer:App-Passwort. WebDAV läuft über
//     /remote.php/dav/files/<user>/:
//
//     nextcloud_url   = https://cloud.example.com
//     nextcloud_token = alice:<App-Passwort>
//
// In beiden Fällen sind alle Pfade der Aktionen relativ zur WebDAV-Wurzel
// (dem geteilten Ordner bzw. dem Datei-Root des Kontos). Ein Ausbruch per
// ".." wird daemon-seitig abgewiesen.

// Config ist die geparste Verbindungs-Konfiguration.
type Config struct {
	// DavBase ist die vollständige WebDAV-Collection-Wurzel inklusive
	// abschließendem Schrägstrich (Aktionen hängen den relativen Pfad an).
	DavBase string
	// User/Pass sind die Basic-Auth-Zugangsdaten (Share-Token bzw. Konto-
	// Benutzer). Pass darf leer sein (passwortlose Freigabe).
	User string
	Pass string
	// Share meldet, ob es sich um eine öffentliche Freigabe (true) oder einen
	// Konto-Zugang (false) handelt — nur für Diagnose/Ausgaben.
	Share bool
}

// passwordSentinels sind Marker-Werte für „keine Passwort" — der Broker
// verlangt, dass nextcloud_token gesetzt ist, eine passwortlose Freigabe hat
// aber keins. Statt eines leeren Secrets hinterlegt der Betreiber einen
// dieser Werte.
var passwordSentinels = map[string]bool{
	"": true, "-": true, "none": true, "kein": true,
	"anonymous": true, "public": true, "x": true,
}

// ParseConfig zerlegt das gebrokerte Credential in die WebDAV-Konfiguration.
func ParseConfig(rawURL, token string) (Config, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return Config{}, fmt.Errorf("nextcloud_url fehlt — den Freigabelink oder die Server-URL hinterlegen")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return Config{}, fmt.Errorf("nextcloud_url: %q ist kein gültiger Link", rawURL)
	}
	origin := u.Scheme + "://" + u.Host

	// Betriebsart 1: Freigabelink (/s/<token>, optional mit /index.php und
	// Installations-Unterverzeichnis davor).
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

	// Betriebsart 2: Konto-Zugang. nextcloud_token = benutzer:app-passwort.
	user, pass, found := strings.Cut(token, ":")
	if !found || user == "" || pass == "" {
		return Config{}, fmt.Errorf("nextcloud_token muss %q sein (Konto-Zugang) — oder nextcloud_url auf einen /s/-Freigabelink zeigen", "benutzer:app-passwort")
	}
	// Ein evtl. mitgegebenes /remote.php/... abschneiden, damit wir sauber
	// auf den Datei-Root des Nutzers zeigen. Installations-Unterverzeichnis
	// (z. B. /nextcloud) bleibt erhalten.
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

// parseShareLink erkennt einen Nextcloud-Freigabelink und liefert das
// Installations-Präfix (leer oder z. B. "/nextcloud") sowie den Share-Token.
func parseShareLink(path string) (prefix, token string, ok bool) {
	idx := strings.Index(path, "/s/")
	if idx < 0 {
		return "", "", false
	}
	token = path[idx+len("/s/"):]
	if i := strings.IndexByte(token, '/'); i >= 0 {
		token = token[:i] // evtl. /download o. Ä. abschneiden
	}
	if token == "" {
		return "", "", false
	}
	prefix = strings.TrimSuffix(path[:idx], "/index.php")
	return prefix, token, true
}
