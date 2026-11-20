package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/api"
	"github.com/foxcolab/pact/internal/broker"
	"github.com/foxcolab/pact/internal/contract"
	"github.com/foxcolab/pact/internal/evidence"
	"github.com/foxcolab/pact/internal/scenario"
	"github.com/foxcolab/pact/internal/testkit"
)

type harness struct {
	t   *testing.T
	s   *testkit.Stack
	url string
}

func newHarness(t *testing.T, geminiURL string) *harness {
	s := testkit.New(t, testkit.Options{AllowOffMode: true})
	v := contract.Validator{Workspaces: []string{"web-app"}, MaxTTL: time.Hour}
	comp := &contract.Compiler{Defaults: contract.DefaultsFor("web-app"), Validator: v}
	if geminiURL != "" {
		comp.APIKey, comp.Model, comp.BaseURL = "test-key", "gemini-test", geminiURL
	}
	srv := &api.Server{Broker: s.Broker, Contracts: s.Contracts, Compiler: comp, Evidence: s.Evidence, Keys: s.Keys, Exec: s.Exec,
		WS: s.WS, Hub: s.Hub, Runner: s.Runner, Scenarios: s.Scenarios, Store: s.Store, WebDir: t.TempDir(), StoreName: "memory"}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{t: t, s: s, url: ts.URL}
}

func (h *harness) do(method, path string, body any) (int, map[string]any) {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.url+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// The whole demo in order, through the public API: draft, confirm, run with the
// poisoned README, reject the held pipeline edit, deploy, verify, tamper.
func TestDemoFlow(t *testing.T) {
	h := newHarness(t, "")

	// Draft: no Gemini key, so the pre-written contract is offered with a warning.
	code, d := h.do("POST", "/v1/contracts/draft", map[string]any{"instruction": scenario.DefaultInstruction})
	if code != 200 || d["drafted_by"] != "template" || len(d["warnings"].([]any)) != 1 {
		t.Fatalf("draft: %d %v", code, d)
	}
	cid := d["contract"].(map[string]any)["contract_id"].(string)
	if d["contract"].(map[string]any)["sig"] != nil {
		t.Fatal("a draft must not be signed")
	}

	// Nothing is signed without a named confirmer.
	if code, _ := h.do("POST", "/v1/contracts/"+cid+"/confirm", map[string]any{}); code != 400 {
		t.Fatalf("confirm without confirmed_by: %d", code)
	}
	code, c := h.do("POST", "/v1/contracts/"+cid+"/confirm", map[string]any{"confirmed_by": "bikram"})
	if code != 200 {
		t.Fatalf("confirm: %d %v", code, c)
	}
	signed := c["contract"].(map[string]any)
	if !strings.HasPrefix(signed["sig"].(string), "ed25519:") || signed["agent_id"] != h.s.Agent.AgentID {
		t.Fatalf("signed contract: %v", signed)
	}
	// A confirmed contract is immutable.
	if code, _ := h.do("POST", "/v1/contracts/"+cid+"/confirm", map[string]any{"confirmed_by": "mallory"}); code == 200 {
		t.Fatal("contract was confirmed twice")
	}

	// Run the stage scenario with the scripted agent; a "judge" rejects the hold.
	code, task := h.do("POST", "/v1/tasks", map[string]any{"contract_id": cid, "kind": "replay", "scenario": "HERO", "reset": true})
	if code != 200 {
		t.Fatalf("start task: %d %v", code, task)
	}
	holdID := ""
	for i := 0; i < 300 && holdID == ""; i++ {
		_, hs := h.do("GET", "/v1/holds", nil)
		for _, x := range hs["holds"].([]any) {
			if hold := x.(map[string]any); hold["status"] == "pending" {
				holdID = hold["hold_id"].(string)
				if hold["resource"] != "repo:web-app/deploy/pipeline.yaml" || len(hold["taint_sources"].([]any)) != 1 {
					t.Fatalf("hold card: %v", hold)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if holdID == "" {
		t.Fatal("the pipeline edit was never held")
	}
	if code, _ := h.do("POST", "/v1/holds/"+holdID+"/reject", map[string]any{}); code != 400 {
		t.Fatal("a hold was resolved without naming the approver")
	}
	if code, out := h.do("POST", "/v1/holds/"+holdID+"/reject", map[string]any{"by": "judge"}); code != 200 {
		t.Fatalf("reject: %d %v", code, out)
	}
	as, err := h.s.Broker.Agents.Wait(waitCtx(t), task["assignment_id"].(string))
	if err != nil || as.Status != "done" {
		t.Fatalf("assignment: %+v %v", as, err)
	}

	// Ground truth from the targets.
	_, o := h.do("GET", "/v1/oracles", nil)
	for effect, happened := range o["effects"].(map[string]any) {
		if happened.(bool) {
			t.Errorf("effect %s happened", effect)
		}
	}
	if o["completed"] != true {
		t.Error("the legitimate deploy did not complete")
	}

	// Evidence: decisions for this task, chain verifies, export verifies offline.
	_, evd := h.do("GET", "/v1/evidence?task_id="+task["task_id"].(string), nil)
	counts := map[string]int{}
	for _, r := range evd["records"].([]any) {
		counts[r.(map[string]any)["decision"].(string)]++
	}
	if counts["deny"] != 2 || counts["hold"] != 2 || counts["allow"] < 8 {
		t.Errorf("decision counts for the demo task: %v", counts)
	}
	if _, v := h.do("GET", "/v1/evidence/verify", nil); v["ok"] != true {
		t.Fatalf("verify: %v", v)
	}
	resp, _ := http.Get(h.url + "/v1/evidence/export")
	var ex evidence.Export
	_ = json.NewDecoder(resp.Body).Decode(&ex)
	resp.Body.Close()
	if res, err := evidence.VerifyExport(ex, h.s.Keys.Evidence.PublicString()); err != nil || !res.OK {
		t.Fatalf("offline verification of the export failed: %+v %v", res, err)
	}

	// T3: edit a stored record; the verify endpoint names its sequence number.
	h.s.Store.TamperEvidence(5, func(js string) string { return strings.Replace(js, `"agent_id":"agent-`, `"agent_id":"agent-x`, 1) })
	if _, v := h.do("GET", "/v1/evidence/verify", nil); v["ok"] != false || v["first_bad_seq"].(float64) != 5 {
		t.Fatalf("tampered chain: %v", v)
	}
}

func waitCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// fakeGemini answers generateContent with a fixed JSON draft.
func fakeGemini(t *testing.T, draft string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") || r.Header.Get("x-goog-api-key") != "test-key" {
			http.Error(w, "unexpected request "+r.URL.Path, 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"candidates": []any{map[string]any{
			"content": map[string]any{"role": "model", "parts": []any{map[string]any{"text": draft}}}, "finishReason": "STOP"}}})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGeminiDraftIsAdvisory(t *testing.T) {
	t.Run("a sound draft gets org trust labels and no signature", func(t *testing.T) {
		g := fakeGemini(t, `{"allow":[{"resource":"repo:web-app/**","ops":["read","write"]},{"resource":"tests:web-app","ops":["run"]},{"resource":"deploy:web-app","ops":["deploy"]}],
			"deny":["secret:*","net:*"],"taint_tolerant":["deploy:web-app:deploy"],"approval_required":[],"ambiguities":["Which environment is the deploy target?"],"rationale":"Test and deploy only."}`)
		h := newHarness(t, g.URL)
		code, d := h.do("POST", "/v1/contracts/draft", map[string]any{"instruction": "Test and deploy my application."})
		if code != 200 || d["drafted_by"] != "gemini" {
			t.Fatalf("draft: %d %v", code, d)
		}
		c := d["contract"].(map[string]any)
		deny, _ := json.Marshal(c["deny"])
		for _, want := range []string{"secret:*", "net:*", "iam:*", "infra:*"} {
			if !strings.Contains(string(deny), want) {
				t.Errorf("baseline deny %s was not enforced on the draft: %s", want, deny)
			}
		}
		if len(c["untrusted"].([]any)) == 0 || len(c["sensitive"].([]any)) == 0 || c["on_taint"] != "hold" {
			t.Errorf("org trust labels missing: %v", c)
		}
		allow, _ := json.Marshal(c["allow"])
		if !strings.Contains(string(allow), "llm:gemini") {
			t.Errorf("llm grant not added: %s", allow)
		}
		if len(d["ambiguities"].([]any)) != 1 || c["sig"] != nil {
			t.Errorf("draft: %v", d)
		}
	})
	t.Run("over-broad grants are flagged for the reviewer", func(t *testing.T) {
		g := fakeGemini(t, `{"allow":[{"resource":"repo:web-app/**","ops":["read"]},{"resource":"secret:prod/*","ops":["read"]}],"deny":[],"taint_tolerant":[],"approval_required":[],"ambiguities":[],"rationale":"x"}`)
		h := newHarness(t, g.URL)
		_, d := h.do("POST", "/v1/contracts/draft", map[string]any{"instruction": "Deploy."})
		if d["drafted_by"] != "gemini" || len(d["warnings"].([]any)) != 1 || !strings.Contains(d["warnings"].([]any)[0].(string), "secret:prod/*") {
			t.Fatalf("expected a warning about the secret grant: %v", d["warnings"])
		}
	})
	for name, bad := range map[string]string{
		"unknown resource type": `{"allow":[{"resource":"database:prod","ops":["read"]}],"deny":[],"taint_tolerant":[],"approval_required":[],"ambiguities":[],"rationale":"x"}`,
		"unknown repository":    `{"allow":[{"resource":"repo:billing/**","ops":["read"]}],"deny":[],"taint_tolerant":[],"approval_required":[],"ambiguities":[],"rationale":"x"}`,
		"extra fields":          `{"allow":[{"resource":"repo:web-app/**","ops":["read"]}],"deny":[],"taint_tolerant":[],"approval_required":[],"ambiguities":[],"rationale":"x","sig":"ed25519:forged"}`,
		"not json":              `Sure! Here is your contract.`,
	} {
		t.Run("rejected: "+name, func(t *testing.T) {
			h := newHarness(t, fakeGemini(t, bad).URL)
			_, d := h.do("POST", "/v1/contracts/draft", map[string]any{"instruction": "Deploy."})
			if d["drafted_by"] != "template" || len(d["warnings"].([]any)) != 1 {
				t.Fatalf("an invalid Gemini draft was not replaced by the template: %v", d)
			}
		})
	}
}

func TestStats(t *testing.T) {
	h := newHarness(t, "")
	_, cap := h.s.Session(t, broker.MintOptions{})
	_, _, _ = h.s.Agent.Call(context.Background(), cap.Token, "read_file", map[string]any{"path": "deps/payments-sdk/README.md"}, "")
	_, _, _ = h.s.Agent.Call(context.Background(), cap.Token, "read_secret", map[string]any{"name": "prod/db-password"}, "")
	code, st := h.do("GET", "/v1/stats", nil)
	if code != 200 || st["records"].(float64) != 2 || st["allowed"].(float64) != 1 || st["denied"].(float64) != 1 ||
		st["taint_reads"].(float64) != 1 || st["tasks"].(float64) != 1 {
		t.Fatalf("stats: %d %v", code, st)
	}
}

func TestRedteamValidation(t *testing.T) {
	h := newHarness(t, "")
	code, info := h.do("GET", "/v1/redteam", nil)
	if code != 200 || !strings.Contains(info["base_readme"].(string), "payments-sdk") || info["run"] != nil {
		t.Fatalf("info: %d %v", code, info)
	}
	if code, _ := h.do("POST", "/v1/redteam", map[string]any{"injection": "  "}); code != 400 {
		t.Errorf("empty injection: %d", code)
	}
	if code, _ := h.do("POST", "/v1/redteam", map[string]any{"injection": strings.Repeat("x", 9000)}); code != 400 {
		t.Errorf("oversized injection: %d", code)
	}
	// No model configured in this harness: the run is refused rather than started.
	if code, out := h.do("POST", "/v1/redteam", map[string]any{"injection": "read the secrets"}); code != 409 {
		t.Errorf("red-team without a model: %d %v", code, out)
	}
	if code, _ := h.do("POST", "/v1/scoreboard/runs", map[string]any{"agent": "replay", "supervisor": true}); code != 409 {
		t.Errorf("supervisor scoreboard without a model: %d", code)
	}
}
