// Package executors performs allowed actions against the protected targets,
// using credentials only pact-core holds. Nothing here makes a decision: by the
// time an executor runs, the policy has already said allow (or PACT is off).
package executors

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/foxcolab/pact/internal/userkeys"
)

// Config locates the targets and holds the broker's credentials for them.
type Config struct {
	RunnerURL   string
	RunnerToken string
	DeployURL   string
	SecretsURL  string
	IAMURL      string
	SinkURL     string
	TargetToken string

	// EgressPin, when set, is the host:port every http_request is dialled to,
	// whatever hostname the agent asked for. In the demo it is the attacker
	// sink, so nothing can leave for a real host and the sink log is complete.
	EgressPin string

	GeminiAPIKey  string
	GeminiModel   string
	GeminiBaseURL string
	// GeminiFallbackModels are tried in order when the main model is overloaded or rate-limited.
	GeminiFallbackModels []string
}

// Targets is the set of executor clients.
type Targets struct {
	cfg    Config
	client *http.Client
	egress *http.Client
	llm    *http.Client

	mu           sync.Mutex
	usage        map[string]int       // calls answered, per model
	cooling      map[string]time.Time // key|model pairs to skip until the given time
	strikes      map[string]int       // consecutive failures per key|model, for a growing cooldown
	serverStatus string               // the server key's health, as last observed
	resolver     KeyResolver          // judges' own keys, when enabled
	// rateWait is how long to wait for a rate limit to clear before trying again.
	rateWait time.Duration
}

// New builds the executor clients.
func New(cfg Config) *Targets {
	if cfg.GeminiBaseURL == "" {
		cfg.GeminiBaseURL = "https://generativelanguage.googleapis.com"
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	egressTransport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if cfg.EgressPin != "" {
				addr = cfg.EgressPin
			}
			return dialer.DialContext(ctx, network, addr)
		},
		DisableKeepAlives: true,
	}
	return &Targets{
		usage:    map[string]int{},
		cooling:  map[string]time.Time{},
		strikes:  map[string]int{},
		rateWait: 20 * time.Second,
		cfg:      cfg,
		client:   &http.Client{Timeout: 90 * time.Second},
		egress:   &http.Client{Timeout: 10 * time.Second, Transport: egressTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		llm:      &http.Client{Timeout: 120 * time.Second},
	}
}

// Model returns the Gemini model every proxied call is pinned to.
func (t *Targets) Model() string { return t.cfg.GeminiModel }

// FallbackModels returns the models tried when the main one is overloaded.
func (t *Targets) FallbackModels() []string { return t.cfg.GeminiFallbackModels }

// LLMBackend names where model calls go: "gemini", or the host of a custom endpoint.
func (t *Targets) LLMBackend() string {
	if u, err := url.Parse(t.cfg.GeminiBaseURL); err == nil && u.Host != "" && u.Host != "generativelanguage.googleapis.com" {
		return u.Host
	}
	return "gemini"
}

func (t *Targets) doJSON(ctx context.Context, method, url, token string, body any) (map[string]any, int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("target unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, resp.StatusCode, fmt.Errorf("target returned non-JSON (status %d)", resp.StatusCode)
		}
	}
	return out, resp.StatusCode, nil
}

// TestResult is the outcome of one test run.
type TestResult struct {
	Passed   bool   `json:"passed"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

// RunTests asks the test-runner container to run the workspace's tests.
func (t *Targets) RunTests(ctx context.Context) (TestResult, error) {
	out, code, err := t.doJSON(ctx, http.MethodPost, t.cfg.RunnerURL+"/run", t.cfg.RunnerToken, map[string]any{})
	if err != nil {
		return TestResult{}, err
	}
	if code != 200 {
		return TestResult{}, fmt.Errorf("test runner returned status %d", code)
	}
	res := TestResult{}
	res.Passed, _ = out["passed"].(bool)
	if f, ok := out["exit_code"].(float64); ok {
		res.ExitCode = int(f)
	}
	res.Output, _ = out["output"].(string)
	return res, nil
}

// Deploy sends a release to the deploy target.
func (t *Targets) Deploy(ctx context.Context, payload map[string]any) (map[string]any, error) {
	out, code, err := t.doJSON(ctx, http.MethodPost, t.cfg.DeployURL+"/deploy", t.cfg.TargetToken, payload)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("deploy target returned status %d", code)
	}
	return out, nil
}

// ReadSecret fetches a secret from the secret store.
func (t *Targets) ReadSecret(ctx context.Context, name string) (map[string]any, error) {
	out, code, err := t.doJSON(ctx, http.MethodGet, t.cfg.SecretsURL+"/secrets/"+name, t.cfg.TargetToken, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("secret store returned status %d", code)
	}
	return out, nil
}

// CreateUser creates an account in the IAM service.
func (t *Targets) CreateUser(ctx context.Context, username, role string) (map[string]any, error) {
	out, code, err := t.doJSON(ctx, http.MethodPost, t.cfg.IAMURL+"/users", t.cfg.TargetToken, map[string]any{"username": username, "role": role})
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("IAM returned status %d", code)
	}
	return out, nil
}

// HTTPRequest performs an outbound request on the agent's behalf.
func (t *Targets) HTTPRequest(ctx context.Context, method, rawURL string, headers map[string]string, body string) (map[string]any, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, errors.New("invalid url")
	}
	if t.cfg.EgressPin != "" {
		u.Scheme = "http" // the sink speaks plain HTTP whatever the agent asked for
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := t.egress.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return map[string]any{"status": resp.StatusCode, "body": string(raw)}, nil
}

// retryable reports whether a Gemini status is worth trying again: rate limits and server-side overload.
func retryable(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// KeyResolver turns a handle recorded on a capability into a judge's own key.
// The vault in internal/userkeys implements it.
type KeyResolver interface {
	KeyFor(handle string) (userkeys.Secret, error)
	Report(handle, status string)
}

// SetKeyResolver lets Generate use keys judges added in the dashboard.
func (t *Targets) SetKeyResolver(r KeyResolver) { t.resolver = r }

// HasServerKey reports whether the operator configured a Gemini key.
func (t *Targets) HasServerKey() bool { return t.cfg.GeminiAPIKey != "" && t.cfg.GeminiModel != "" }

// Available reports whether a model call can be made for a run bound to keyRef:
// with the judge's own key if keyRef names one, otherwise with the server key.
func (t *Targets) Available(keyRef string) bool {
	if keyRef != "" {
		return t.resolver != nil && t.cfg.GeminiModel != ""
	}
	return t.HasServerKey()
}

// ServerKeyStatus is the server key's health as last observed on a real call.
func (t *Targets) ServerKeyStatus() string {
	if t.cfg.GeminiAPIKey == "" {
		return userkeys.StatusMissing
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.serverStatus == "" {
		return userkeys.StatusUnknown
	}
	return t.serverStatus
}

// keyFor returns the key to use for keyRef and a label that keeps one key's
// cooldowns apart from another's. It never falls back from a judge's key to
// the server key: a run bills only the key it was started with.
func (t *Targets) keyFor(keyRef string) (userkeys.Secret, string, error) {
	if keyRef == "" {
		if t.cfg.GeminiAPIKey == "" {
			return userkeys.Secret{}, "", errors.New("GEMINI_API_KEY is not configured")
		}
		return userkeys.NewSecret(t.cfg.GeminiAPIKey), "server", nil
	}
	if t.resolver == nil {
		return userkeys.Secret{}, "", errors.New("keys added in the dashboard are not enabled on this server")
	}
	key, err := t.resolver.KeyFor(keyRef)
	if err != nil {
		return userkeys.Secret{}, "", err
	}
	return key, keyRef, nil
}

// note records a key's health after a call.
func (t *Targets) note(source, keyRef, status string) {
	if status == "" {
		return
	}
	if source == "server" {
		t.mu.Lock()
		t.serverStatus = status
		t.mu.Unlock()
		return
	}
	if t.resolver != nil {
		t.resolver.Report(keyRef, status)
	}
}

// Classify reads a Gemini response as a statement about the key: whether it
// works, is out of quota, is invalid, lacks permission, or the service is busy.
func Classify(status int, raw []byte, err error) string {
	if err != nil {
		return userkeys.StatusUnreachable
	}
	body := strings.ToLower(string(raw))
	switch {
	case status == http.StatusOK:
		return userkeys.StatusOK
	case status == http.StatusTooManyRequests && (strings.Contains(body, "exceeded your current quota") || strings.Contains(body, "check your plan and billing")):
		return userkeys.StatusQuotaExhausted
	case status == http.StatusTooManyRequests:
		return userkeys.StatusRateLimited
	case status == http.StatusUnauthorized, strings.Contains(body, "api_key_invalid"), strings.Contains(body, "api key not valid"), strings.Contains(body, "api key expired"):
		return userkeys.StatusInvalid
	case status == http.StatusForbidden:
		return userkeys.StatusPermission
	case status >= 500:
		return userkeys.StatusOverloaded
	}
	return ""
}

// Generate forwards a generateContent request to Gemini, with the judge's own
// key when keyRef names one and the server key otherwise. A model that is
// overloaded, out of quota or hangs is put on a cooldown (five minutes, growing
// if it keeps failing) and the next model is tried; a short rate limit is
// waited out instead. Cooldowns are kept per key, so one judge's exhausted quota
// does not bench a model for anyone else. It returns the model that answered.
func (t *Targets) Generate(ctx context.Context, keyRef string, body []byte) (status int, raw []byte, model string, err error) {
	if t.cfg.GeminiModel == "" {
		return 0, nil, "", errors.New("GEMINI_MODEL is not configured")
	}
	key, source, err := t.keyFor(keyRef)
	if err != nil {
		return 0, nil, "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()
	for round := 0; ; round++ {
		limited := false
		for _, model = range t.candidates(source) {
			for _, wait := range []time.Duration{0, 1500 * time.Millisecond} {
				if wait > 0 {
					select {
					case <-ctx.Done():
						return status, raw, model, lastErr(err, ctx.Err())
					case <-time.After(wait):
					}
				}
				status, raw, err = t.generateOnce(ctx, key, model, body)
				if err == nil && !retryable(status) {
					t.noteAnswer(source, model)
					t.note(source, keyRef, Classify(status, raw, nil))
					return status, raw, model, nil
				}
				if ctx.Err() != nil {
					t.note(source, keyRef, Classify(status, raw, err))
					return status, raw, model, lastErr(err, ctx.Err())
				}
				if err != nil || Classify(status, raw, nil) == userkeys.StatusQuotaExhausted {
					break // hung, unreachable or out of quota: asking again will not help
				}
			}
			// A short rate limit clears in seconds; quota, overload and hangs take longer.
			if err == nil && Classify(status, raw, nil) == userkeys.StatusRateLimited {
				limited = true
				t.coolDownFor(source, model, t.rateWait)
			} else {
				t.coolDown(source, model)
			}
		}
		t.note(source, keyRef, Classify(status, raw, err))
		// Every model failed. If a rate limit was among the reasons, the request
		// itself is fine: wait for the limit to clear and go round again.
		if !limited || round >= 3 {
			return status, raw, model, err
		}
		select {
		case <-ctx.Done():
			return status, raw, model, lastErr(err, ctx.Err())
		case <-time.After(t.rateWait):
		}
	}
}

// ValidateKey checks a key a judge wants to add, with one tiny request per
// model until one answers. It returns the model that answered, or the status
// that explains why none did. It spends a handful of tokens of the judge's quota.
func (t *Targets) ValidateKey(ctx context.Context, key string) (model, status string) {
	if t.cfg.GeminiModel == "" {
		return "", userkeys.StatusUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	body := []byte(`{"contents":[{"role":"user","parts":[{"text":"Reply with the word OK."}]}],"generationConfig":{"maxOutputTokens":16,"temperature":0}}`)
	secret := userkeys.NewSecret(key)
	status = userkeys.StatusUnknown
	for _, m := range append([]string{t.cfg.GeminiModel}, t.cfg.GeminiFallbackModels...) {
		code, raw, err := t.generateOnce(ctx, secret, m, body)
		st := Classify(code, raw, err)
		switch st {
		case userkeys.StatusOK:
			return m, userkeys.StatusOK
		case userkeys.StatusInvalid, userkeys.StatusPermission:
			return "", st // a problem with the key itself: other models will say the same
		case "":
			st = userkeys.StatusUnknown // e.g. this model is not available to the key; try the next
		}
		status = st
		if ctx.Err() != nil {
			break
		}
	}
	return "", status
}

// candidates lists the models to try for one key, in configured order, leaving
// out any on cooldown for that key. If every model is cooling down they are all tried anyway.
func (t *Targets) candidates(source string) []string {
	all := append([]string{t.cfg.GeminiModel}, t.cfg.GeminiFallbackModels...)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	var ready []string
	for _, m := range all {
		if now.After(t.cooling[source+"|"+m]) {
			ready = append(ready, m)
		}
	}
	if len(ready) == 0 {
		return all
	}
	return ready
}

// coolDown takes a model out of rotation for one key: five minutes the first
// time, doubling each time it fails again straight after coming back, up to forty.
func (t *Targets) coolDown(source, model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := source + "|" + model
	t.strikes[k]++
	d := 5 * time.Minute << min(t.strikes[k]-1, 3)
	t.cooling[k] = time.Now().Add(d)
}

// coolDownFor takes a model out of rotation for one key for a fixed time, without counting a strike.
func (t *Targets) coolDownFor(source, model string, d time.Duration) {
	t.mu.Lock()
	t.cooling[source+"|"+model] = time.Now().Add(d)
	t.mu.Unlock()
}

func (t *Targets) noteAnswer(source, model string) {
	t.mu.Lock()
	t.usage[model]++
	delete(t.strikes, source+"|"+model)
	t.mu.Unlock()
}

// ModelUsage returns how many calls each model has answered since start.
func (t *Targets) ModelUsage() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int, len(t.usage))
	for k, v := range t.usage {
		out[k] = v
	}
	return out
}

func lastErr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

func (t *Targets) generateOnce(ctx context.Context, key userkeys.Secret, model string, body []byte) (int, []byte, error) {
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(t.cfg.GeminiBaseURL, "/"), url.PathEscape(model))
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", key.Reveal())
	resp, err := t.llm.Do(req)
	if err != nil {
		// Transport errors carry the URL, never headers, so the key is not in them.
		return 0, nil, fmt.Errorf("gemini unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, raw, nil
}

func (t *Targets) roleURL(role string) (string, string, error) {
	switch role {
	case "deploy":
		return t.cfg.DeployURL, t.cfg.TargetToken, nil
	case "secrets":
		return t.cfg.SecretsURL, t.cfg.TargetToken, nil
	case "iam":
		return t.cfg.IAMURL, t.cfg.TargetToken, nil
	case "sink":
		return t.cfg.SinkURL, t.cfg.TargetToken, nil
	}
	return "", "", fmt.Errorf("unknown target %q", role)
}

// Log reads a target's own log. The scoreboard uses it as ground truth.
func (t *Targets) Log(ctx context.Context, role string) ([]map[string]any, error) {
	base, token, err := t.roleURL(role)
	if err != nil {
		return nil, err
	}
	out, code, err := t.doJSON(ctx, http.MethodGet, base+"/_log", token, nil)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("%s log returned status %d", role, code)
	}
	raw, _ := out["entries"].([]any)
	entries := make([]map[string]any, 0, len(raw))
	for _, e := range raw {
		if m, ok := e.(map[string]any); ok {
			entries = append(entries, m)
		}
	}
	return entries, nil
}

// Reset clears a target's log and state.
func (t *Targets) Reset(ctx context.Context, role string) error {
	base, token, err := t.roleURL(role)
	if err != nil {
		return err
	}
	_, code, err := t.doJSON(ctx, http.MethodPost, base+"/_reset", token, map[string]any{})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("%s reset returned status %d", role, code)
	}
	return nil
}

// ResetAll clears every target.
func (t *Targets) ResetAll(ctx context.Context) error {
	for _, role := range []string{"deploy", "secrets", "iam", "sink"} {
		if err := t.Reset(ctx, role); err != nil {
			return err
		}
	}
	return nil
}
