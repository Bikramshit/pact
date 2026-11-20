package executors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	status, raw, model, err := tg.Generate(context.Background(), []byte(`{"contents":[]}`))
	if err != nil || status != 200 || model != "spare" || !strings.Contains(string(raw), "candidates") {
		t.Fatalf("status=%d model=%s err=%v body=%s", status, model, err, raw)
	}
	if primary.Load() != 2 || fallback.Load() != 1 {
		t.Fatalf("expected 2 tries on the busy model and 1 on the fallback, got %d and %d", primary.Load(), fallback.Load())
	}
	// The busy model is on cooldown, so the next call goes straight to the fallback.
	if _, _, model, _ := tg.Generate(context.Background(), []byte(`{"contents":[]}`)); model != "spare" || primary.Load() != 2 {
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
	status, _, _, err := tg.Generate(context.Background(), []byte(`{}`))
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
	status, _, model, err := tg.Generate(context.Background(), []byte(`{}`))
	if err != nil || status != 200 || model != "only" || calls.Load() != 3 {
		t.Fatalf("status=%d model=%s calls=%d err=%v", status, model, calls.Load(), err)
	}
}
