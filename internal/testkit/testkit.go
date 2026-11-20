// Package testkit assembles a complete in-process PACT stack for acceptance
// tests: real broker, policy, evidence chain and mock targets over HTTP, an
// in-memory store, and a Go agent speaking the same wire protocol as the
// Python one.
package testkit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/foxcolab/pact/internal/agentclient"
	"github.com/foxcolab/pact/internal/broker"
	"github.com/foxcolab/pact/internal/broker/executors"
	"github.com/foxcolab/pact/internal/contract"
	"github.com/foxcolab/pact/internal/events"
	"github.com/foxcolab/pact/internal/evidence"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/mocktargets"
	"github.com/foxcolab/pact/internal/policy"
	"github.com/foxcolab/pact/internal/scenario"
	"github.com/foxcolab/pact/internal/scoreboard"
	"github.com/foxcolab/pact/internal/store"
	"github.com/foxcolab/pact/internal/workspace"
)

// Stack is a running test deployment.
type Stack struct {
	Store     *store.Memory
	Keys      *keys.Set
	Contracts *contract.Registry
	Broker    *broker.Broker
	Evidence  *evidence.Engine
	Exec      *executors.Targets
	WS        *workspace.Workspace
	Hub       *events.Hub
	Runner    *scoreboard.Runner
	Scenarios []scenario.Scenario
	BrokerURL string
	Agent     *agentclient.Client
}

// Options adjusts the stack.
type Options struct {
	Mode         string
	AllowOffMode bool
	HoldTimeout  time.Duration
	PolicyEngine string
	NoAgent      bool
	// StandInModel serves the scripted stand-in for the Gemini API, so code paths
	// that call a model (the AI-supervisor baseline) can run in tests.
	StandInModel bool
}

// RepoRoot walks up from the working directory to the module root.
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above the test directory")
		}
		dir = parent
	}
}

// New builds and starts a stack; everything is torn down when the test ends.
func New(t testing.TB, opts Options) *Stack {
	t.Helper()
	ctx := context.Background()
	root := RepoRoot(t)
	const token = "test-target-token"

	serve := func(h http.Handler) *httptest.Server {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv
	}
	deploy := serve(mocktargets.NewHandler("deploy", token))
	secrets := serve(mocktargets.NewHandler("secrets", token))
	iam := serve(mocktargets.NewHandler("iam", token))
	sink := serve(mocktargets.NewHandler("sink", token))

	ws := workspace.New(t.TempDir(), "web-app", filepath.Join(root, "demo", "web-app"))
	if err := ws.Reset(nil); err != nil {
		t.Fatal(err)
	}
	runner := serve(testRunner(ws))

	st := store.NewMemory()
	ks, err := keys.LoadOrGenerate("")
	if err != nil {
		t.Fatal(err)
	}
	reg := &contract.Registry{Store: st, Org: ks.Org, Validator: contract.Validator{Workspaces: []string{"web-app"}, MaxTTL: time.Hour}}
	dec, err := policy.New(ctx, opts.PolicyEngine)
	if err != nil {
		t.Fatal(err)
	}
	ev := evidence.New(st, ks.Evidence)
	hub := events.NewHub()
	cfg := executors.Config{
		RunnerURL: runner.URL, RunnerToken: "test-runner-token",
		DeployURL: deploy.URL, SecretsURL: secrets.URL, IAMURL: iam.URL, SinkURL: sink.URL, TargetToken: token,
		EgressPin: strings.TrimPrefix(sink.URL, "http://"),
	}
	if opts.StandInModel {
		cfg.GeminiAPIKey, cfg.GeminiModel, cfg.GeminiBaseURL = "test-key", "stand-in", serve(mocktargets.NewHandler("fakegemini", "")).URL
	}
	ex := executors.New(cfg)
	br := broker.New(broker.Config{Mode: opts.Mode, AllowOffMode: opts.AllowOffMode, HoldTimeout: opts.HoldTimeout}, st, ks, reg, dec, ev, ex, ws, hub)
	brokerSrv := serve(br.Handler())

	scs, err := scenario.Load(filepath.Join(root, "demo", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Stack{Store: st, Keys: ks, Contracts: reg, Broker: br, Evidence: ev, Exec: ex, WS: ws, Hub: hub, Scenarios: scs, BrokerURL: brokerSrv.URL}
	s.Runner = &scoreboard.Runner{Broker: br, Contracts: reg, Exec: ex, WS: ws, Evidence: ev, Store: st, Hub: hub, Scenarios: scs,
		Defaults: contract.DefaultsFor("web-app")}

	if !opts.NoAgent {
		ag, err := agentclient.New(brokerSrv.URL)
		if err != nil {
			t.Fatal(err)
		}
		if err := ag.Register(ctx); err != nil {
			t.Fatal(err)
		}
		actx, cancel := context.WithCancel(ctx)
		t.Cleanup(cancel)
		go ag.Serve(actx)
		s.Agent = ag
	}
	return s
}

// Session confirms the pre-written contract for the stack's agent and mints a capability.
func (s *Stack) Session(t testing.TB, opts broker.MintOptions) (contract.Contract, broker.CapabilityRecord) {
	t.Helper()
	c := contract.Template(scenario.DefaultInstruction, contract.DefaultsFor("web-app"), time.Now())
	c.AgentID = s.Agent.AgentID
	signed, err := s.Contracts.Confirm(context.Background(), c, "tester")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Broker.MintCapability(context.Background(), signed, opts)
	if err != nil {
		t.Fatal(err)
	}
	return signed, rec
}

// testRunner stands in for the test-runner container: it runs the workspace's
// unit tests with the local python3, or checks for the fix when python3 is absent.
func testRunner(ws *workspace.Workspace) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-runner-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		passed, output, code := false, "", 1
		if py, err := exec.LookPath("python3"); err == nil {
			cmd := exec.Command(py, "-m", "unittest", "discover", "-s", "tests")
			cmd.Dir = ws.Dir()
			cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
			out, err := cmd.CombinedOutput()
			output, passed = string(out), err == nil
		} else {
			src, _ := ws.Read("app/cart.py")
			passed = strings.Contains(string(src), "discount_percent / 100")
			output = "python3 not available; checked for the fix instead"
		}
		if passed {
			code = 0
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"passed": passed, "exit_code": code, "output": output})
	})
}
