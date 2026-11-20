package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const version = "projects/p/locations/global/keyRings/pact/cryptoKeys/org/cryptoKeyVersions/1"

// fakeKMS implements the two Cloud KMS REST calls PACT uses, for one key version.
func fakeKMS(t *testing.T, algorithm string, corrupt bool) (*httptest.Server, ed25519.PublicKey, *int) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(pub)
	pemText := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
	signs := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/"+version+"/publicKey":
			_ = json.NewEncoder(w).Encode(map[string]string{"pem": pemText, "algorithm": algorithm})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/"+version+":asymmetricSign":
			var body struct {
				Data string `json:"data"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			msg, _ := base64.StdEncoding.DecodeString(body.Data)
			signs++
			sig := ed25519.Sign(priv, msg)
			if corrupt {
				sig[0] ^= 0xff
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND","message":"no such key"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return srv, pub, &signs
}

func TestKMSKeySignsRemotely(t *testing.T) {
	srv, pub, signs := fakeKMS(t, "EC_SIGN_ED25519", false)
	k := NewKMSWithClient(srv.Client(), srv.URL)
	kp, err := k.Key(context.Background(), "org", version)
	if err != nil {
		t.Fatal(err)
	}
	if kp.Backend != KMSBackend || !kp.Public.Equal(pub) {
		t.Fatalf("unexpected key: backend=%s", kp.Backend)
	}
	sig, err := kp.Sign([]byte("sha256:abc"))
	if err != nil || !Verify(pub, []byte("sha256:abc"), sig) || *signs != 1 {
		t.Fatalf("remote signature did not verify: %v (signs=%d)", err, *signs)
	}
}

func TestKMSRejectsWrongAlgorithmAndBadSignatures(t *testing.T) {
	srv, _, _ := fakeKMS(t, "EC_SIGN_P256_SHA256", false)
	if _, err := NewKMSWithClient(srv.Client(), srv.URL).Key(context.Background(), "org", version); err == nil || !strings.Contains(err.Error(), "EC_SIGN_ED25519") {
		t.Fatalf("a non-Ed25519 key was accepted: %v", err)
	}
	bad, _, _ := fakeKMS(t, "EC_SIGN_ED25519", true)
	kp, err := NewKMSWithClient(bad.Client(), bad.URL).Key(context.Background(), "org", version)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kp.Sign([]byte("x")); err == nil {
		t.Fatal("a signature that does not verify was returned to the caller")
	}
	if _, err := NewKMSWithClient(srv.Client(), srv.URL).Key(context.Background(), "org", "projects/p/cryptoKeys/org"); err == nil {
		t.Fatal("a key name without a version was accepted")
	}
}

func TestEvidenceKeyIsCertifiedByTheOrgKey(t *testing.T) {
	set, err := LoadOrGenerate("")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyEvidenceCert(set.Org.Public, set.Evidence.PublicString(), set.EvidenceCert) {
		t.Fatal("local key set: certificate does not verify")
	}
	// Moving the org key into KMS re-certifies the evidence key under the new org key.
	srv, pub, _ := fakeKMS(t, "EC_SIGN_ED25519", false)
	if err := set.UseKMS(context.Background(), NewKMSWithClient(srv.Client(), srv.URL), version, ""); err != nil {
		t.Fatal(err)
	}
	if set.Org.Backend != KMSBackend || set.Issuer.Backend != "local" {
		t.Fatalf("backends: org=%s issuer=%s", set.Org.Backend, set.Issuer.Backend)
	}
	if !VerifyEvidenceCert(pub, set.Evidence.PublicString(), set.EvidenceCert) {
		t.Fatal("certificate was not re-issued by the KMS org key")
	}
	other, _ := Generate("other")
	if VerifyEvidenceCert(pub, other.PublicString(), set.EvidenceCert) {
		t.Fatal("certificate verified for a different evidence key")
	}
}

func TestRemoteSignerErrorsAreReturned(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	kp := Remote("org", KMSBackend, pub, func([]byte) ([]byte, error) { return nil, errors.New("permission denied") })
	if _, err := kp.Sign([]byte("x")); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("remote failure was swallowed: %v", err)
	}
}
