package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNs sends through Apple's push service with a token-based key: the .p8
// file from the developer account, its key id and the team id. The request
// carries a short-lived ES256 JWT signed with it; Apple asks for a new one
// at least every hour and not more often than every twenty minutes.
type APNs struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string
	topic  string // the app's bundle id
	client *http.Client

	// Hosts, replaceable in tests.
	Production  string
	Development string

	mu     sync.Mutex
	token  string
	issued time.Time
}

// NewAPNs reads the key file.
func NewAPNs(keyFile, keyID, teamID, topic string) (*APNs, error) {
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("APNs key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("APNs key: not a PEM file (expected the .p8 from the developer account)")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("APNs key: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("APNs key: not an EC key")
	}
	return &APNs{
		key: ec, keyID: keyID, teamID: teamID, topic: topic,
		client:      &http.Client{Timeout: 15 * time.Second},
		Production:  "https://api.push.apple.com",
		Development: "https://api.sandbox.push.apple.com",
	}, nil
}

func (a *APNs) bearer() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Since(a.issued) < 40*time.Minute {
		return a.token, nil
	}
	t := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": a.teamID, "iat": time.Now().Unix()})
	t.Header["kid"] = a.keyID
	s, err := t.SignedString(a.key)
	if err != nil {
		return "", err
	}
	a.token, a.issued = s, time.Now()
	return s, nil
}

func (a *APNs) Send(ctx context.Context, m Message) error {
	host := a.Production
	if m.Environment == "development" {
		host = a.Development
	}
	alert := map[string]string{"title": m.Title}
	if m.Body != "" {
		alert["body"] = m.Body
	}
	aps := map[string]any{"alert": alert, "badge": m.Badge, "thread-id": m.AgentID}
	if m.Sound != "" {
		aps["sound"] = m.Sound
	}
	payload, _ := json.Marshal(map[string]any{"aps": aps, "agent_id": m.AgentID})
	bearer, err := a.bearer()
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, host+"/3/device/"+m.Token, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("authorization", "bearer "+bearer)
	req.Header.Set("apns-topic", a.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var why struct {
		Reason string `json:"reason"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	_ = json.Unmarshal(body, &why)
	switch {
	case resp.StatusCode == http.StatusGone,
		why.Reason == "BadDeviceToken", why.Reason == "Unregistered", why.Reason == "DeviceTokenNotForTopic":
		return ErrGone
	case why.Reason == "ExpiredProviderToken":
		a.mu.Lock()
		a.token = ""
		a.mu.Unlock()
	}
	return fmt.Errorf("APNs: HTTP %d %s", resp.StatusCode, why.Reason)
}
