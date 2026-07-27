package llm

import (
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
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// newVertex builds a Gemini client that talks to Vertex AI on GCP. It reuses the
// generateContent body but signs in with a service account: a JWT signed by the
// account's private key is exchanged for a short-lived OAuth token. Credentials
// come from the provider's api_key (a path to the service-account JSON or the
// JSON itself) or GOOGLE_APPLICATION_CREDENTIALS; the region from provider
// config or a default.
func newVertex(o Options) (Client, error) {
	sa, err := loadServiceAccount(o.APIKey)
	if err != nil {
		return nil, err
	}
	region := strings.TrimSpace(o.Region)
	if region == "" {
		region = firstNonEmpty(os.Getenv("GOOGLE_CLOUD_REGION"), os.Getenv("CLOUD_ML_REGION"), "us-central1")
	}
	project := firstNonEmpty(os.Getenv("GOOGLE_CLOUD_PROJECT"), sa.ProjectID)
	if project == "" {
		return nil, errors.New("vertex needs a GCP project: set GOOGLE_CLOUD_PROJECT or use a service-account key that carries project_id")
	}
	base := o.BaseURL
	if base == "" {
		base = fmt.Sprintf("https://%s-aiplatform.googleapis.com", region)
	}
	o.BaseURL = strings.TrimRight(base, "/")
	return &geminiClient{
		opts:    o,
		vertex:  true,
		project: project,
		region:  region,
		tokens:  &gcpTokenSource{sa: sa, client: o.HTTPClient},
	}, nil
}

// serviceAccount is the subset of a GCP service-account key we use.
type serviceAccount struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
	ProjectID   string `json:"project_id"`
}

// loadServiceAccount reads the key from a literal JSON string, a file path, or
// GOOGLE_APPLICATION_CREDENTIALS.
func loadServiceAccount(keyOrPath string) (*serviceAccount, error) {
	raw := strings.TrimSpace(keyOrPath)
	var data []byte
	switch {
	case strings.HasPrefix(raw, "{"):
		data = []byte(raw)
	case raw != "":
		b, err := os.ReadFile(raw)
		if err != nil {
			return nil, fmt.Errorf("reading service-account key %q: %w", raw, err)
		}
		data = b
	default:
		path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		if path == "" {
			return nil, errors.New("vertex needs a service account: set the provider api_key to the key JSON/path or GOOGLE_APPLICATION_CREDENTIALS")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading GOOGLE_APPLICATION_CREDENTIALS: %w", err)
		}
		data = b
	}
	var sa serviceAccount
	if err := json.Unmarshal(data, &sa); err != nil {
		return nil, fmt.Errorf("parsing service-account key: %w", err)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, errors.New("service-account key is missing client_email or private_key")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &sa, nil
}

// gcpTokenSource mints and caches OAuth access tokens from a service account.
type gcpTokenSource struct {
	sa     *serviceAccount
	client *http.Client

	mu      sync.Mutex
	cached  string
	expires time.Time
}

// token returns a valid access token, refreshing shortly before expiry.
func (t *gcpTokenSource) token() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cached != "" && time.Now().Before(t.expires.Add(-1*time.Minute)) {
		return t.cached, nil
	}
	tok, ttl, err := t.mint()
	if err != nil {
		return "", err
	}
	t.cached = tok
	t.expires = time.Now().Add(time.Duration(ttl) * time.Second)
	return tok, nil
}

func (t *gcpTokenSource) mint() (string, int, error) {
	key, err := parseRSAKey(t.sa.PrivateKey)
	if err != nil {
		return "", 0, err
	}
	now := time.Now()
	header := b64url(`{"alg":"RS256","typ":"JWT"}`)
	claims := fmt.Sprintf(`{"iss":%q,"scope":"https://www.googleapis.com/auth/cloud-platform","aud":%q,"iat":%d,"exp":%d}`,
		t.sa.ClientEmail, t.sa.TokenURI, now.Unix(), now.Add(time.Hour).Unix())
	signingInput := header + "." + b64url(claims)

	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", 0, err
	}
	assertion := signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)

	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", t.sa.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := t.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, err
	}
	if out.AccessToken == "" {
		return "", 0, fmt.Errorf("token exchange failed: %s %s", out.Error, out.ErrorDesc)
	}
	if out.ExpiresIn <= 0 {
		out.ExpiresIn = 3600
	}
	return out.AccessToken, out.ExpiresIn, nil
}

// parseRSAKey parses a PEM private key, PKCS#8 or PKCS#1.
func parseRSAKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, errors.New("service-account private_key is not valid PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing private key: %w", err)
	}
	rsaKey, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("service-account key is not an RSA key")
	}
	return rsaKey, nil
}

func b64url(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
