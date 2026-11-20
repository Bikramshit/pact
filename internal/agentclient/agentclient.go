// Package agentclient is a Go implementation of the agent side of the PACT
// protocol: it holds its own Ed25519 key, registers the public half, signs every
// request and can play back a scripted assignment. The acceptance tests use it
// in place of the Python agent container; the wire protocol is identical.
package agentclient

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/foxcolab/pact/internal/broker"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/scenario"
)

// Client is one agent instance.
type Client struct {
	BrokerURL string
	AgentID   string
	key       *keys.KeyPair
	http      *http.Client
	last      *http.Request
	lastBody  []byte
}

// New creates an agent with a fresh key pair.
func New(brokerURL string) (*Client, error) {
	k, err := keys.Generate("agent")
	if err != nil {
		return nil, err
	}
	return &Client{BrokerURL: strings.TrimRight(brokerURL, "/"), key: k, http: &http.Client{Timeout: 150 * time.Second}}, nil
}

// PublicKey returns the agent's public key string.
func (c *Client) PublicKey() string { return c.key.PublicString() }

// Register sends the public key to the broker and stores the assigned agent id.
func (c *Client) Register(ctx context.Context) error {
	var out broker.Agent
	if _, err := c.plain(ctx, http.MethodPost, "/v1/agents", map[string]string{"pubkey": c.PublicKey()}, &out); err != nil {
		return err
	}
	c.AgentID = out.AgentID
	return nil
}

func (c *Client) plain(ctx context.Context, method, path string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BrokerURL+path, rd)
	if err != nil {
		return 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNoContent {
		return resp.StatusCode, fmt.Errorf("%s %s: status %d: %s", method, path, resp.StatusCode, raw)
	}
	if out != nil && len(raw) > 0 {
		return resp.StatusCode, json.Unmarshal(raw, out)
	}
	return resp.StatusCode, nil
}

// Generate makes one signed model call through the broker's Gemini proxy.
func (c *Client) Generate(ctx context.Context, token string, request any) (int, map[string]any, error) {
	body, _ := json.Marshal(map[string]any{"request": request})
	return c.signed(ctx, token, "/v1/llm/generate", body)
}

func (c *Client) signed(ctx context.Context, token, path string, body []byte) (int, map[string]any, error) {
	nb := make([]byte, 12)
	_, _ = rand.Read(nb)
	ts, nonce := time.Now().UTC().Format(time.RFC3339), hex.EncodeToString(nb)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BrokerURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(broker.HeaderCapability, token)
	req.Header.Set(broker.HeaderTimestamp, ts)
	req.Header.Set(broker.HeaderNonce, nonce)
	sig, err := c.key.Sign([]byte(broker.SigningString(http.MethodPost, path, body, ts, nonce)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set(broker.HeaderSignature, sig)
	return c.do(req)
}

// Call makes one signed tool call. tamper may be "", "capability_scope" or "reuse_nonce".
func (c *Client) Call(ctx context.Context, token, tool string, args map[string]any, tamper string) (int, map[string]any, error) {
	if tamper == "reuse_nonce" && c.last != nil {
		// Resend the previous signed request byte for byte, optionally under
		// another capability (the signature does not cover it).
		req := c.last.Clone(ctx)
		req.Body = io.NopCloser(bytes.NewReader(c.lastBody))
		if token != "" {
			req.Header.Set(broker.HeaderCapability, token)
		}
		return c.do(req)
	}
	if tamper == "capability_scope" {
		token = WidenScope(token, "secret:*:read")
	}
	path := "/v1/tools/" + tool
	if args == nil {
		args = map[string]any{}
	}
	body, _ := json.Marshal(map[string]any{"args": args})
	nb := make([]byte, 12)
	_, _ = rand.Read(nb)
	ts, nonce := time.Now().UTC().Format(time.RFC3339), hex.EncodeToString(nb)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BrokerURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(broker.HeaderCapability, token)
	req.Header.Set(broker.HeaderTimestamp, ts)
	req.Header.Set(broker.HeaderNonce, nonce)
	sig, err := c.key.Sign([]byte(broker.SigningString(http.MethodPost, path, body, ts, nonce)))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set(broker.HeaderSignature, sig)
	c.last, c.lastBody = req.Clone(context.Background()), body
	return c.do(req)
}

func (c *Client) do(req *http.Request) (int, map[string]any, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out, nil
}

// WidenScope rewrites the payload of a capability token to add a scope while
// keeping the original signature, as an agent trying to escalate would.
func WidenScope(token, scope string) string {
	seg, sig, ok := strings.Cut(token, ".")
	if !ok {
		return token
	}
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return token
	}
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return token
	}
	scopes, _ := p["scopes"].([]any)
	p["scopes"] = append(scopes, scope)
	edited, _ := json.Marshal(p)
	return base64.RawURLEncoding.EncodeToString(edited) + "." + sig
}

// Classify turns a broker response into the decision the agent observed.
func Classify(status int, body map[string]any) (decision string, reasons []string, held bool) {
	held, _ = body["held"].(bool)
	if rr, ok := body["reasons"].([]any); ok {
		for _, r := range rr {
			if s, ok := r.(string); ok {
				reasons = append(reasons, s)
			}
		}
	}
	switch {
	case status == http.StatusOK && held:
		return "hold_approved", reasons, true
	case status == http.StatusOK:
		return "allow", reasons, false
	case status == http.StatusForbidden && held:
		return "hold_rejected", reasons, true
	case status == http.StatusForbidden:
		return "deny", reasons, false
	}
	return "error", reasons, held
}

// RunReplay plays a scripted assignment and reports what the broker answered.
func (c *Client) RunReplay(ctx context.Context, as broker.Assignment) broker.Report {
	rep := broker.Report{Status: "completed", Steps: []broker.StepReport{}}
	for _, st := range as.Steps {
		status, body, err := c.Call(ctx, as.Capability, st.Tool, st.Args, st.Tamper)
		sr := broker.StepReport{Tool: st.Tool, Status: status}
		if err != nil {
			sr.Decision, sr.Error = "error", err.Error()
			rep.Status, rep.Error = "failed", err.Error()
		} else {
			sr.Decision, sr.Reasons, sr.Held = Classify(status, body)
			if e, ok := body["error"].(string); ok {
				sr.Error = e
			}
		}
		rep.Steps = append(rep.Steps, sr)
	}
	rep.Summary = fmt.Sprintf("replayed %d scripted tool calls", len(rep.Steps))
	return rep
}

// Serve polls for assignments and plays scripted ones until ctx ends.
func (c *Client) Serve(ctx context.Context) {
	for ctx.Err() == nil {
		var as broker.Assignment
		status, err := c.plain(ctx, http.MethodGet, "/v1/agents/"+c.AgentID+"/assignment", nil, &as)
		if err != nil || status == http.StatusNoContent || as.ID == "" {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
			continue
		}
		rep := broker.Report{Status: "failed", Error: "the Go test agent only plays scripted assignments"}
		if as.Kind == "replay" {
			rep = c.RunReplay(ctx, as)
		}
		_, _ = c.plain(ctx, http.MethodPost, "/v1/agents/"+c.AgentID+"/assignments/"+as.ID+"/done", rep, nil)
	}
}

// CheckStep compares an observed decision with a scenario step's expectation.
func CheckStep(st scenario.Step, got broker.StepReport) (ok bool, why string) {
	want := st.Expect
	if want == "" {
		return true, ""
	}
	match := got.Decision == want || (want == "hold" && got.Held)
	if !match {
		return false, fmt.Sprintf("expected %s, got %s %v", want, got.Decision, got.Reasons)
	}
	if st.ExpectReason != "" {
		for _, r := range got.Reasons {
			if strings.Contains(r, st.ExpectReason) {
				return true, ""
			}
		}
		return false, fmt.Sprintf("expected reason %q, got %v", st.ExpectReason, got.Reasons)
	}
	return true, ""
}
