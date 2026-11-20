package keys

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"cloud.google.com/go/auth/credentials"
	"cloud.google.com/go/auth/httptransport"
)

// KMSBackend names keys held in Google Cloud KMS.
const KMSBackend = "cloud-kms"

const kmsEndpoint = "https://cloudkms.googleapis.com"

// KMS signs with Ed25519 keys held in Google Cloud KMS. The private key never
// leaves Google's service; pact-core only ever sees public keys and signatures.
type KMS struct {
	client   *http.Client
	endpoint string
}

// NewKMS builds a client from Application Default Credentials (for example a
// service-account file named by GOOGLE_APPLICATION_CREDENTIALS).
func NewKMS(ctx context.Context) (*KMS, error) {
	creds, err := credentials.DetectDefault(&credentials.DetectOptions{Scopes: []string{"https://www.googleapis.com/auth/cloudkms"}})
	if err != nil {
		return nil, fmt.Errorf("keys: Google Cloud credentials not found: %w", err)
	}
	client, err := httptransport.NewClient(&httptransport.Options{Credentials: creds})
	if err != nil {
		return nil, err
	}
	client.Timeout = 20 * time.Second
	return &KMS{client: client, endpoint: kmsEndpoint}, nil
}

// NewKMSWithClient is for tests: it talks to endpoint with the given client.
func NewKMSWithClient(client *http.Client, endpoint string) *KMS {
	return &KMS{client: client, endpoint: strings.TrimRight(endpoint, "/")}
}

func (k *KMS) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, k.endpoint+"/v1/"+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := k.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &apiErr)
		return fmt.Errorf("Cloud KMS returned %d %s: %s", resp.StatusCode, apiErr.Error.Status, apiErr.Error.Message)
	}
	return json.Unmarshal(raw, out)
}

// Key returns a signing key backed by one KMS key version, named in full:
// projects/P/locations/L/keyRings/R/cryptoKeys/K/cryptoKeyVersions/V.
// The key must use the EC_SIGN_ED25519 algorithm.
func (k *KMS) Key(ctx context.Context, name, resource string) (*KeyPair, error) {
	if !strings.Contains(resource, "/cryptoKeyVersions/") {
		return nil, fmt.Errorf("keys: %q is not a key version (it must end in /cryptoKeyVersions/N)", resource)
	}
	var pk struct {
		PEM       string `json:"pem"`
		Algorithm string `json:"algorithm"`
	}
	if err := k.do(ctx, http.MethodGet, resource+"/publicKey", nil, &pk); err != nil {
		return nil, fmt.Errorf("keys: reading the %s public key: %w", name, err)
	}
	if pk.Algorithm != "EC_SIGN_ED25519" {
		return nil, fmt.Errorf("keys: the %s key uses %s; PACT needs EC_SIGN_ED25519", name, pk.Algorithm)
	}
	block, _ := pem.Decode([]byte(pk.PEM))
	if block == nil {
		return nil, errors.New("keys: Cloud KMS returned a public key that is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("keys: parsing the %s public key: %w", name, err)
	}
	pub, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("keys: the %s key is not an Ed25519 key", name)
	}
	sign := func(msg []byte) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		var out struct {
			Signature string `json:"signature"`
		}
		// Ed25519 in Cloud KMS signs the message itself, sent in "data", not a digest.
		if err := k.do(ctx, http.MethodPost, resource+":asymmetricSign", map[string]string{"data": base64.StdEncoding.EncodeToString(msg)}, &out); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(out.Signature)
	}
	return Remote(name, KMSBackend, pub, sign), nil
}

// UseKMS replaces the org and issuer keys with KMS-held keys and re-certifies
// the evidence key. An empty resource name leaves that key local.
func (s *Set) UseKMS(ctx context.Context, k *KMS, orgResource, issuerResource string) error {
	if orgResource != "" {
		kp, err := k.Key(ctx, "org", orgResource)
		if err != nil {
			return err
		}
		s.Org = kp
	}
	if issuerResource != "" {
		kp, err := k.Key(ctx, "issuer", issuerResource)
		if err != nil {
			return err
		}
		s.Issuer = kp
	}
	return s.Certify()
}
