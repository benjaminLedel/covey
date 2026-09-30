package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"covey/internal/settings"
)

// FCM sends through Firebase Cloud Messaging (#424, #431) with a service
// account of the app's Firebase project: the JSON file from the Firebase
// console. Google takes an OAuth access token, which the instance gets by
// signing a JWT with the account's key and exchanging it at the token URI; it
// lasts an hour.
//
// It is the one direct sender, for both platforms. To Android goes a data
// message: the app builds the notification itself, so that it plays the
// chosen sound and groups by agent the same way it does on the iPhone. To the
// iPhone goes a message Firebase hands to Apple as it stands — alert, badge,
// sound, thread — so that iOS shows it without the app running.
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

// NewFCM takes a service account as settings.ParseServiceAccount read it.
func NewFCM(sa settings.ServiceAccount) *FCM {
	return &FCM{
		key: sa.Key, email: sa.ClientEmail, projectID: sa.ProjectID, tokenURI: sa.TokenURI,
		client:   &http.Client{Timeout: 15 * time.Second},
		Endpoint: "https://fcm.googleapis.com",
	}
}

// ParseFCM reads a service account key file's content.
func ParseFCM(raw string) (*FCM, error) {
	sa, err := settings.ParseServiceAccount(raw)
	if err != nil {
		return nil, fmt.Errorf("FCM credentials: %w", err)
	}
	return NewFCM(sa), nil
}

// ProjectID is the Firebase project the account belongs to.
func (f *FCM) ProjectID() string { return f.projectID }

// Check fetches a fresh access token: the account is valid and Google knows
// it. It needs no device, and says nothing about whether the project holds
// an APNs key — only Apple answers that, on the first notification.
func (f *FCM) Check(ctx context.Context) error {
	f.mu.Lock()
	f.token = ""
	f.mu.Unlock()
	_, err := f.bearer(ctx)
	return err
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
	payload, _ := json.Marshal(map[string]any{"message": fcmMessage(m)})
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

// fcmMessage is the message for the device's platform.
func fcmMessage(m Message) map[string]any {
	if m.Platform == "android" {
		return map[string]any{
			"token": m.Token,
			"data": map[string]string{
				"title": m.Title, "body": m.Body, "agent_id": m.AgentID, "conversation_id": m.ConversationID,
				"sound": m.Sound, "badge": strconv.Itoa(m.Badge),
			},
			"android": map[string]any{"priority": "high"},
		}
	}
	// The payload Apple gets: what the direct APNs sender used to send
	// (#379), agent_id beside aps, where the app reads it from userInfo.
	alert := map[string]string{"title": m.Title}
	if m.Body != "" {
		alert["body"] = m.Body
	}
	// A conversation that is no agent's thread groups under its own id.
	group := m.AgentID
	if group == "" {
		group = m.ConversationID
	}
	aps := map[string]any{"alert": alert, "badge": m.Badge, "thread-id": group}
	if m.Sound != "" {
		aps["sound"] = m.Sound
	}
	return map[string]any{
		"token": m.Token,
		"apns": map[string]any{
			"headers": map[string]string{"apns-priority": "10", "apns-push-type": "alert"},
			"payload": map[string]any{"aps": aps, "agent_id": m.AgentID, "conversation_id": m.ConversationID},
		},
	}
}
