package teamsapp

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"strings"
)

//go:embed assets/color.png
var colorIcon []byte

//go:embed assets/outline-source.png
var outlineSource []byte

// PackageVersion is the manifest version of the app package. Teams refuses
// to update an uploaded app whose version did not grow, so it moves with
// every change to the manifest below.
const PackageVersion = "1.1.0"

// Package builds the Teams app package for this installation: manifest.json
// with the app ID and the installation's address, and the two icons.
func (s *Service) Package(siteURL, name string) ([]byte, error) {
	if s == nil {
		return nil, errNoApp
	}
	host := siteURL
	if u, err := url.Parse(siteURL); err == nil && u.Host != "" {
		host = u.Hostname()
	}
	if strings.TrimSpace(name) == "" {
		name = "covey"
	}
	manifest := map[string]any{
		"$schema":         "https://developer.microsoft.com/en-us/json-schemas/teams/v1.17/MicrosoftTeams.schema.json",
		"manifestVersion": "1.17",
		"version":         PackageVersion,
		"id":              s.cfg.AppID,
		"developer": map[string]any{
			"name":          "covey",
			"websiteUrl":    siteURL,
			"privacyUrl":    siteURL,
			"termsOfUseUrl": siteURL,
		},
		"name": map[string]any{"short": name, "full": name + " — AI colleagues"},
		"description": map[string]any{
			"short": "Your organisation's covey agents in Teams",
			"full":  "Write to your organisation's covey agents in a chat or mention them in a channel. Agents answer and post in the channels they are allowed to use.",
		},
		"icons":       map[string]any{"color": "color.png", "outline": "outline.png"},
		"accentColor": "#2A5B9E",
		"bots": []map[string]any{{
			"botId":              s.cfg.AppID,
			"scopes":             []string{"personal", "team", "groupChat"},
			"supportsFiles":      false,
			"isNotificationOnly": false,
		}},
		"permissions":  []string{"identity", "messageTeamMembers"},
		"validDomains": []string{host},
	}
	mj, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	outline, err := outlineIcon()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range []struct {
		name string
		data []byte
	}{{"manifest.json", mj}, {"color.png", colorIcon}, {"outline.png", outline}} {
		w, err := zw.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// outlineIcon is the 32×32 icon Teams wants for its sidebar: white on
// transparent, the shape taken from the favicon's alpha.
func outlineIcon() ([]byte, error) {
	src, err := png.Decode(bytes.NewReader(outlineSource))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			_, _, _, a := src.At(b.Min.X+x, b.Min.Y+y).RGBA()
			out.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8(a >> 8)})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
