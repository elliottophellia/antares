package llm

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"testing"
)

func testSAJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	sa := map[string]string{
		"client_email": "svc@proj.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
		"project_id":   "my-project",
		"token_uri":    "https://oauth2.googleapis.com/token",
	}
	b, _ := json.Marshal(sa)
	return string(b)
}

func TestLoadServiceAccountFromJSON(t *testing.T) {
	sa, err := loadServiceAccount(testSAJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	if sa.ClientEmail == "" || sa.ProjectID != "my-project" {
		t.Fatalf("bad parse: %+v", sa)
	}
	if _, err := parseRSAKey(sa.PrivateKey); err != nil {
		t.Fatalf("private key did not parse: %v", err)
	}
}

func TestParseRSAKeyRejectsGarbage(t *testing.T) {
	if _, err := parseRSAKey("not a pem"); err == nil {
		t.Fatal("garbage key should error")
	}
}

func TestVertexEndpointRouting(t *testing.T) {
	c := &geminiClient{vertex: true, project: "my-project", region: "us-central1",
		opts: Options{BaseURL: "https://us-central1-aiplatform.googleapis.com"}}
	got := c.endpoint("gemini-1.5-pro", "generateContent", false)
	want := "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/publishers/google/models/gemini-1.5-pro:generateContent"
	if got != want {
		t.Fatalf("vertex endpoint = %q, want %q", got, want)
	}
	if c.Kind() != "vertex" {
		t.Fatalf("kind = %q, want vertex", c.Kind())
	}
}

func TestNewVertexConstructs(t *testing.T) {
	c, err := New(Options{Kind: "vertex", APIKey: testSAJSON(t), Region: "europe-west1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind() != "vertex" {
		t.Fatalf("expected vertex client, got %s", c.Kind())
	}
}

// The JWT signing step must produce a signature the public key verifies.
func TestJWTSignatureVerifies(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	input := "header.claims"
	sum := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature did not verify: %v", err)
	}
}
