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

	mu      sync.Mutex
	usage   map[string]int       // calls answered, per model
	cooling map[string]time.Time // models to skip until the given time
	strikes map[string]int       // consecutive failures, for a growing cooldown
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

// LLMConfigured reports whether a Gemini API key is present.
func (t *Targets) LLMConfigured() bool { return t.cfg.GeminiAPIKey != "" }

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

// Generate forwards a generateContent request to Gemini with the broker's API
// key. A model that is overloaded, rate-limited or hangs is put on a cooldown
// (five minutes, growing if it keeps failing; twenty seconds for a rate limit,
// which is then waited out) and the next model is tried, so a busy model costs one
// slow call rather than failing a task. The configured model is used again as
// soon as its cooldown ends. It returns the model that answered.
func (t *Targets) Generate(ctx context.Context, body []byte) (status int, raw []byte, model string, err error) {
	if t.cfg.GeminiAPIKey == "" {
		return 0, nil, "", errors.New("GEMINI_API_KEY is not configured")
	}
	if t.cfg.GeminiModel == "" {
		return 0, nil, "", errors.New("GEMINI_MODEL is not configured")
	}
	ctx, cancel := context.WithTimeout(ctx, 130*time.Second)
	defer cancel()
	for round := 0; ; round++ {
		limited := false
		for _, model = range t.candidates() {
			for _, wait := range []time.Duration{0, 1500 * time.Millisecond} {
				if wait > 0 {
					select {
					case <-ctx.Done():
						return status, raw, model, lastErr(err, ctx.Err())
					case <-time.After(wait):
					}
				}
				status, raw, err = t.generateOnce(ctx, model, body)
				if err == nil && !retryable(status) {
					t.noteAnswer(model)
					return status, raw, model, nil
				}
				if ctx.Err() != nil {
					return status, raw, model, lastErr(err, ctx.Err())
				}
				if err != nil {
					break // hung or unreachable: do not wait on it a second time
				}
			}
			// A rate limit clears in seconds; overload and hangs take minutes.
			if err == nil && status == http.StatusTooManyRequests {
				limited = true
				t.coolDownFor(model, t.rateWait)
			} else {
				t.coolDown(model)
			}
		}
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

// candidates lists the models to try, in configured order, leaving out any on
// cooldown. If every model is cooling down they are all tried anyway.
func (t *Targets) candidates() []string {
	all := append([]string{t.cfg.GeminiModel}, t.cfg.GeminiFallbackModels...)
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	var ready []string
	for _, m := range all {
		if now.After(t.cooling[m]) {
			ready = append(ready, m)
		}
	}
	if len(ready) == 0 {
		return all
	}
	return ready
}

// coolDown takes a model out of rotation: five minutes the first time, doubling
// each time it fails again straight after coming back, up to forty.
func (t *Targets) coolDown(model string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.strikes[model]++
	d := 5 * time.Minute << min(t.strikes[model]-1, 3)
	t.cooling[model] = time.Now().Add(d)
}

// coolDownFor takes a model out of rotation for a fixed time without counting a strike.
func (t *Targets) coolDownFor(model string, d time.Duration) {
	t.mu.Lock()
	t.cooling[model] = time.Now().Add(d)
	t.mu.Unlock()
}

func (t *Targets) noteAnswer(model string) {
	t.mu.Lock()
	t.usage[model]++
	delete(t.strikes, model)
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

func (t *Targets) generateOnce(ctx context.Context, model string, body []byte) (int, []byte, error) {
	endpoint := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(t.cfg.GeminiBaseURL, "/"), url.PathEscape(model))
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", t.cfg.GeminiAPIKey)
	resp, err := t.llm.Do(req)
	if err != nil {
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
