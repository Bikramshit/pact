package executors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/userkeys"
)

// An overloaded model is retried, then the fallback models are tried in order.
func TestGenerateRetriesThenFallsBack(t *testing.T) {
	var primary, fallback atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "k" {
			t.Errorf("API key not sent")
		}
		switch {
		case strings.Contains(r.URL.Path, "/models/busy:"):
			primary.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"status":"UNAVAILABLE"}}`))
		case strings.Contains(r.URL.Path, "/models/spare:"):
			fallback.Add(1)
			_, _ = w.Write([]byte(`{"candidates":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	tg := New(Config{GeminiAPIKey: "k", GeminiModel: "busy", GeminiFallbackModels: []string{"spare"}, GeminiBaseURL: srv.URL})
	status, raw, model, err := tg.Generate(context.Background(), "", []byte(`{"contents":[]}`))
	if err != nil || status != 200 || model != "spare" || !strings.Contains(string(raw), "candidates") {
		t.Fatalf("status=%d model=%s err=%v body=%s", status, model, err, raw)
	}
	if primary.Load() != 2 || fallback.Load() != 1 {
		t.Fatalf("expected 2 tries on the busy model and 1 on the fallback, got %d and %d", primary.Load(), fallback.Load())
	}
	// The busy model is on cooldown, so the next call goes straight to the fallback.
	if _, _, model, _ := tg.Generate(context.Background(), "", []byte(`{"contents":[]}`)); model != "spare" || primary.Load() != 2 {
		t.Fatalf("second call should go straight to the fallback: model=%s busy-model calls=%d", model, primary.Load())
	}
	if u := tg.ModelUsage(); u["spare"] != 2 || u["busy"] != 0 {
		t.Fatalf("usage: %v", u)
	}
}

// A request the model rejects outright (bad request, permission) is not retried.
func TestGenerateDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400}}`))
	}))
	defer srv.Close()
	tg := New(Config{GeminiAPIKey: "k", GeminiModel: "m", GeminiFallbackModels: []string{"n"}, GeminiBaseURL: srv.URL})
	status, _, _, err := tg.Generate(context.Background(), "", []byte(`{}`))
	if err != nil || status != 400 || calls.Load() != 1 {
		t.Fatalf("status=%d calls=%d err=%v", status, calls.Load(), err)
	}
}

// A rate limit is waited out rather than treated as a dead model.
func TestGenerateWaitsOutARateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) <= 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srv.Close()
	tg := New(Config{GeminiAPIKey: "k", GeminiModel: "only", GeminiBaseURL: srv.URL})
	tg.rateWait = 30 * time.Millisecond
	status, _, model, err := tg.Generate(context.Background(), "", []byte(`{}`))
	if err != nil || status != 200 || model != "only" || calls.Load() != 3 {
		t.Fatalf("status=%d model=%s calls=%d err=%v", status, model, calls.Load(), err)
	}
}

type fakeResolver struct {
	keys    map[string]string
	reports map[string]string
}

func (f *fakeResolver) KeyFor(h string) (userkeys.Secret, error) {
	k, ok := f.keys[h]
	if !ok {
		return userkeys.Secret{}, userkeys.ErrExpired
	}
	return userkeys.NewSecret(k), nil
}
func (f *fakeResolver) Report(h, st string) { f.reports[h] = st }

// Each run's model calls carry its own key; one key's exhausted quota does not
// bench a model for another key; an expired handle never falls back to the server key.
func TestKeysArePerRun(t *testing.T) {
	var mu sync.Mutex
	seen := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		k := r.Header.Get("x-goog-api-key")
		mu.Lock()
		seen = append(seen, k)
		mu.Unlock()
		if k == "judge-key-out-of-quota-000000" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"code":429,"message":"You exceeded your current quota, please check your plan and billing details.","status":"RESOURCE_EXHAUSTED"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}))
	defer srv.Close()
	res := &fakeResolver{keys: map[string]string{"uk_a": "judge-key-aaaaaaaaaaaaaaaaaaaa", "uk_q": "judge-key-out-of-quota-000000"}, reports: map[string]string{}}
	tg := New(Config{GeminiAPIKey: "server-key-ssssssssssssssssss", GeminiModel: "m", GeminiBaseURL: srv.URL})
	tg.SetKeyResolver(res)

	if _, _, _, err := tg.Generate(context.Background(), "uk_a", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := tg.Generate(context.Background(), "", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	status, _, _, _ := tg.Generate(context.Background(), "uk_q", []byte(`{}`))
	if status != http.StatusTooManyRequests || res.reports["uk_q"] != userkeys.StatusQuotaExhausted {
		t.Fatalf("quota-exhausted key: status=%d report=%q", status, res.reports["uk_q"])
	}
	// The exhausted key's cooldown must not affect the server key or the other judge.
	if st, _, _, err := tg.Generate(context.Background(), "", []byte(`{}`)); err != nil || st != 200 {
		t.Fatalf("server key was benched by another key's quota: %d %v", st, err)
	}
	mu.Lock()
	n := len(seen)
	mu.Unlock()
	if _, _, _, err := tg.Generate(context.Background(), "uk_gone", []byte(`{}`)); err != userkeys.ErrExpired {
		t.Fatalf("expired handle: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != n {
		t.Fatal("an expired judge key fell back to another key")
	}
	if seen[0] != "judge-key-aaaaaaaaaaaaaaaaaaaa" || seen[1] != "server-key-ssssssssssssssssss" {
		t.Fatalf("wrong keys sent: %v", seen[:2])
	}
	if tg.ServerKeyStatus() != userkeys.StatusOK || res.reports["uk_a"] != userkeys.StatusOK {
		t.Fatalf("health: server=%s judge=%s", tg.ServerKeyStatus(), res.reports["uk_a"])
	}
}

func TestClassify(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{200, `{}`, userkeys.StatusOK},
		{429, `{"error":{"message":"You exceeded your current quota, please check your plan and billing details."}}`, userkeys.StatusQuotaExhausted},
		{429, `{"error":{"message":"Resource has been exhausted (e.g. check quota)."}}`, userkeys.StatusRateLimited},
		{400, `{"error":{"message":"API key not valid. Please pass a valid API key.","details":[{"reason":"API_KEY_INVALID"}]}}`, userkeys.StatusInvalid},
		{403, `{"error":{"status":"PERMISSION_DENIED"}}`, userkeys.StatusPermission},
		{503, `{"error":{"status":"UNAVAILABLE"}}`, userkeys.StatusOverloaded},
		{404, `{"error":{"status":"NOT_FOUND"}}`, ""},
	}
	for _, c := range cases {
		if got := Classify(c.status, []byte(c.body), nil); got != c.want {
			t.Errorf("%d %s: got %q want %q", c.status, c.body, got, c.want)
		}
	}
}

// Validation stops at a key problem, and moves to the next model past a model problem.
func TestValidateKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch k := r.Header.Get("x-goog-api-key"); {
		case k == "bad-key-xxxxxxxxxxxxxxxxxxxxx":
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"message":"API key not valid."}}`))
		case strings.Contains(r.URL.Path, "/models/main:"):
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"error":{"message":"You exceeded your current quota"}}`))
		default:
			_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"OK"}]}}]}`))
		}
	}))
	defer srv.Close()
	tg := New(Config{GeminiModel: "main", GeminiFallbackModels: []string{"lite"}, GeminiBaseURL: srv.URL})
	if m, st := tg.ValidateKey(context.Background(), "good-key-xxxxxxxxxxxxxxxxxxxx"); st != userkeys.StatusOK || m != "lite" {
		t.Fatalf("good key: %s %s", m, st)
	}
	if m, st := tg.ValidateKey(context.Background(), "bad-key-xxxxxxxxxxxxxxxxxxxxx"); st != userkeys.StatusInvalid || m != "" {
		t.Fatalf("bad key: %s %s", m, st)
	}
}
