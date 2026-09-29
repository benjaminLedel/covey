package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// FCM sends through Firebase Cloud Messaging (#424) with a service account
// of the app's Firebase project: the JSON file from the Firebase console.
// Google takes an OAuth access token, which the instance gets by signing a
// JWT with the account's key and exchanging it at the token URI; it lasts an
// hour.
//
// What goes out is a data message: the app builds the notification itself,
// so that it plays the chosen sound and groups by agent the same way it does
// on the iPhone.
type FCM struct {
	key       *rsa.PrivateKey
	email     string
	projectID string
	tokenURI  string
	client    *http.Client

	// Endpoint, replaceable in tests.
	Endpoint string

	mu      sync.Mutex
	token   string
	expires time.Time
}

const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// NewFCM reads the service account file.
func NewFCM(credentialsFile string) (*FCM, error) {
	raw, err := os.ReadFile(credentialsFile)
	if err != nil {
		return nil, fmt.Errorf("FCM credentials: %w", err)
	}
	var sa struct {
		Type        string `json:"type"`
		ProjectID   string `json:"project_id"`
		PrivateKey  string `json:"private_key"`
		ClientEmail string `json:"client_email"`
		TokenURI    string `json:"token_uri"`
	}
	if err := json.Unmarshal(raw, &sa); err != nil {
		return nil, fmt.Errorf("FCM credentials: %w", err)
	}
	if sa.Type != "service_account" || sa.ProjectID == "" || sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("FCM credentials: expected a service account key file (type service_account, project_id, client_email, private_key)")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	block, _ := pem.Decode([]byte(sa.PrivateKey))
	if block == nil {
		return nil, errors.New("FCM credentials: private_key is not PEM")
	}
	var rk *rsa.PrivateKey
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		var ok bool
		if rk, ok = k.(*rsa.PrivateKey); !ok {
			return nil, errors.New("FCM credentials: private_key is not an RSA key")
		}
	} else if rk, err = x509.ParsePKCS1PrivateKey(block.Bytes); err != nil {
		return nil, fmt.Errorf("FCM credentials: %w", err)
	}
	return &FCM{
		key: rk, email: sa.ClientEmail, projectID: sa.ProjectID, tokenURI: sa.TokenURI,
		client:   &http.Client{Timeout: 15 * time.Second},
		Endpoint: "https://fcm.googleapis.com",
	}, nil
}

// bearer is the access token, fetched anew a few minutes before it runs out.
func (f *FCM) bearer(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.token != "" && time.Now().Before(f.expires.Add(-5*time.Minute)) {
		return f.token, nil
	}
	now := time.Now()
	assertion, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": f.email, "scope": fcmScope, "aud": f.tokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}).SignedString(f.key)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		return "", fmt.Errorf("FCM token: HTTP %d %s", resp.StatusCode, out.Error)
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	f.token, f.expires = out.AccessToken, now.Add(time.Duration(out.ExpiresIn)*time.Second)
	return f.token, nil
}

func (f *FCM) Send(ctx context.Context, m Message) error {
	data := map[string]string{
		"title": m.Title, "body": m.Body, "agent_id": m.AgentID,
		"sound": m.Sound, "badge": strconv.Itoa(m.Badge),
	}
	payload, _ := json.Marshal(map[string]any{"message": map[string]any{
		"token":   m.Token,
		"data":    data,
		"android": map[string]any{"priority": "high"},
	}})
	bearer, err := f.bearer(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.Endpoint+"/v1/projects/"+url.PathEscape(f.projectID)+"/messages:send", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil
	}
	var why struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	_ = json.Unmarshal(body, &why)
	code := why.Error.Status
	for _, d := range why.Error.Details {
		if d.ErrorCode != "" {
			code = d.ErrorCode
		}
	}
	switch {
	case resp.StatusCode == http.StatusNotFound, code == "UNREGISTERED",
		// A token that is no token at all; other invalid arguments are ours.
		code == "INVALID_ARGUMENT" && strings.Contains(strings.ToLower(why.Error.Message), "registration token"):
		return ErrGone
	case resp.StatusCode == http.StatusUnauthorized:
		f.mu.Lock()
		f.token = ""
		f.mu.Unlock()
	}
	return fmt.Errorf("FCM: HTTP %d %s", resp.StatusCode, code)
}
