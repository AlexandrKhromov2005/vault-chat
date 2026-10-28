package push

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	fcmScope    = "https://www.googleapis.com/auth/firebase.messaging"
	fcmTokenURI = "https://oauth2.googleapis.com/token"
	fcmEndpoint = "https://fcm.googleapis.com"
	// MaxCredentialsBytes bounds the service account file.
	MaxCredentialsBytes = 64 << 10
	// maxResponseBytes bounds what is read from Google's answers.
	maxResponseBytes = 64 << 10
)

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`)

// FCM sends data-only messages through Firebase Cloud Messaging HTTP v1,
// authorized by a service account (docs/push.md). It never sends text.
type FCM struct {
	projectID string
	email     string
	key       *rsa.PrivateKey
	client    *http.Client
	tokenURI  string
	endpoint  string
	now       func() time.Time

	mu          sync.Mutex
	accessToken string
	expiresAt   time.Time
}

type serviceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// NewFCM parses a service account JSON file. Its token_uri must be Google's:
// the signed assertion goes nowhere else.
func NewFCM(data []byte) (*FCM, error) {
	if len(data) > MaxCredentialsBytes {
		return nil, errors.New("FCM credentials file is too large")
	}
	var account serviceAccount
	if err := json.Unmarshal(data, &account); err != nil {
		return nil, errors.New("FCM credentials are not a service account JSON file")
	}
	if account.Type != "service_account" || !projectIDPattern.MatchString(account.ProjectID) || account.ClientEmail == "" {
		return nil, errors.New("FCM credentials lack a valid type, project_id or client_email")
	}
	if account.TokenURI != "" && account.TokenURI != fcmTokenURI {
		return nil, errors.New("FCM credentials name an unexpected token_uri")
	}
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return nil, errors.New("FCM credentials lack a PEM private key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("FCM private key is not PKCS#8")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok || key.N.BitLen() < 2048 {
		return nil, errors.New("FCM private key is not an RSA key of at least 2048 bits")
	}
	return &FCM{projectID: account.ProjectID, email: account.ClientEmail, key: key,
		client: &http.Client{Timeout: 10 * time.Second}, tokenURI: fcmTokenURI, endpoint: fcmEndpoint, now: time.Now}, nil
}

type fcmMessage struct {
	Message struct {
		Token   string            `json:"token"`
		Data    map[string]string `json:"data"`
		Android struct {
			Priority    string `json:"priority"`
			CollapseKey string `json:"collapse_key"`
			TTL         string `json:"ttl"`
		} `json:"android"`
	} `json:"message"`
}

// Send delivers a sync signal for one room to one device.
func (f *FCM) Send(ctx context.Context, m Message) error {
	access, err := f.access(ctx)
	if err != nil {
		return err
	}
	var body fcmMessage
	body.Message.Token = m.Token
	body.Message.Data = map[string]string{"type": "sync", "room_id": m.RoomID}
	body.Message.Android.Priority = "HIGH"
	body.Message.Android.CollapseKey = "room:" + m.RoomID
	body.Message.Android.TTL = "3600s"
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode FCM message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		f.endpoint+"/v1/projects/"+f.projectID+"/messages:send", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build FCM request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("send FCM message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusUnauthorized {
		f.mu.Lock()
		f.accessToken = ""
		f.mu.Unlock()
	}
	code := fcmErrorCode(answer)
	switch {
	case code == "UNREGISTERED", resp.StatusCode == http.StatusNotFound, code == "SENDER_ID_MISMATCH":
		return ErrTokenInvalid
	default:
		// Neither the token nor the answer body is logged or returned.
		return fmt.Errorf("FCM answered %d %s", resp.StatusCode, code)
	}
}

// fcmErrorCode extracts the FCM error code from an error answer.
func fcmErrorCode(answer []byte) string {
	var e struct {
		Error struct {
			Status  string `json:"status"`
			Details []struct {
				ErrorCode string `json:"errorCode"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(answer, &e) != nil {
		return ""
	}
	for _, d := range e.Error.Details {
		if d.ErrorCode != "" {
			return sanitizeCode(d.ErrorCode)
		}
	}
	return sanitizeCode(e.Error.Status)
}

// sanitizeCode keeps codes like UNREGISTERED and drops anything else.
func sanitizeCode(code string) string {
	if len(code) > 40 || strings.Trim(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") != "" {
		return ""
	}
	return code
}

// access returns a cached OAuth access token, exchanging a fresh signed
// assertion a minute before the old one expires.
func (f *FCM) access(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.now()
	if f.accessToken != "" && now.Before(f.expiresAt.Add(-time.Minute)) {
		return f.accessToken, nil
	}
	assertion, err := f.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build FCM token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request FCM access token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode != http.StatusOK || json.Unmarshal(answer, &token) != nil || token.AccessToken == "" || token.ExpiresIn <= 0 {
		return "", fmt.Errorf("FCM access token request answered %d", resp.StatusCode)
	}
	f.accessToken = token.AccessToken
	f.expiresAt = now.Add(time.Duration(min(token.ExpiresIn, 3600)) * time.Second)
	return f.accessToken, nil
}

// assertion is the RS256 JWT of the service account for the messaging scope.
func (f *FCM) assertion(now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{
		"iss": f.email, "scope": fcmScope, "aud": fcmTokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("encode FCM assertion: %w", err)
	}
	signed := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign FCM assertion: %w", err)
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}
