// Command pact-core is the trusted side of PACT: control plane on one port,
// execution broker on another, with the contract registry, capability issuer,
// policy verifier, evidence engine and LLM proxy as internal packages.
//
//	pact-core                  serve (default)
//	pact-core verify FILE      verify an exported evidence bundle offline
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/foxcolab/pact/internal/api"
	"github.com/foxcolab/pact/internal/broker"
	"github.com/foxcolab/pact/internal/broker/executors"
	"github.com/foxcolab/pact/internal/contract"
	"github.com/foxcolab/pact/internal/events"
	"github.com/foxcolab/pact/internal/evidence"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/netguard"
	"github.com/foxcolab/pact/internal/policy"
	"github.com/foxcolab/pact/internal/scenario"
	"github.com/foxcolab/pact/internal/scoreboard"
	"github.com/foxcolab/pact/internal/store"
	"github.com/foxcolab/pact/internal/workspace"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("pact-core: ")
	if len(os.Args) > 1 && os.Args[1] == "verify" {
		os.Exit(verifyCmd(os.Args[2:]))
	}
	if err := serve(); err != nil {
		log.Fatal(err)
	}
}

// verifyCmd checks an exported evidence bundle without contacting pact-core.
func verifyCmd(args []string) int {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	pubkey := fs.String("pubkey", "", "evidence public key to verify against (default: the key named in the bundle)")
	orgKey := fs.String("org-pubkey", "", "org public key: also check that it vouches for the evidence key")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: pact-core verify [-pubkey ed25519:...] FILE")
		return 2
	}
	raw, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var ex evidence.Export
	if err := json.Unmarshal(raw, &ex); err != nil {
		fmt.Fprintln(os.Stderr, "not an evidence export:", err)
		return 2
	}
	res, err := evidence.VerifyExport(ex, *pubkey)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	if !res.OK {
		return 1
	}
	if *orgKey != "" {
		orgPub, err := keys.DecodePublic(*orgKey)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		if !keys.VerifyEvidenceCert(orgPub, res.PublicKey, ex.KeyCertificate) {
			fmt.Fprintln(os.Stderr, "the org key does not vouch for the evidence key that signed this chain")
			return 1
		}
		fmt.Println("evidence key certified by the given org key")
	}
	return 0
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Store.
	var st store.Store
	storeName := "memory"
	if url := os.Getenv("PACT_DB_URL"); url != "" {
		pg, err := store.OpenPostgres(ctx, url)
		if err != nil {
			return err
		}
		st, storeName = pg, "postgresql"
	} else {
		log.Print("PACT_DB_URL is not set: using the in-memory store; nothing survives a restart")
		st = store.NewMemory()
	}
	defer st.Close()

	// Keys: org (contracts), issuer (capabilities), evidence (records).
	keyDir := ""
	if data := os.Getenv("PACT_DATA_DIR"); data != "" {
		keyDir = filepath.Join(data, "keys")
	}
	ks, err := keys.LoadOrGenerate(keyDir)
	if err != nil {
		return err
	}
	// Optionally hold the org key (contracts) and issuer key (capabilities) in
	// Cloud KMS. They sign once per task. The evidence key stays local, vouched
	// for by the org key, so recording a decision does not wait on the network.
	if orgKey, issuerKey := os.Getenv("PACT_KMS_ORG_KEY"), os.Getenv("PACT_KMS_ISSUER_KEY"); orgKey != "" || issuerKey != "" {
		kms, err := keys.NewKMS(ctx)
		if err != nil {
			return err
		}
		if err := ks.UseKMS(ctx, kms, orgKey, issuerKey); err != nil {
			return err
		}
	}

	wsName := env("PACT_WORKSPACE_NAME", "web-app")
	ws := workspace.New(env("PACT_WORKSPACE_DIR", filepath.Join(os.TempDir(), "pact-workspace")), wsName, env("PACT_SEED_DIR", "demo/web-app"))
	if !ws.Ready() {
		if err := ws.Reset(nil); err != nil {
			return fmt.Errorf("populating workspace: %w", err)
		}
	}
	scenarios, err := scenario.Load(env("PACT_SCENARIOS_DIR", "demo/scenarios"))
	if err != nil {
		return err
	}

	dec, err := policy.New(ctx, os.Getenv("PACT_POLICY_ENGINE"))
	if err != nil {
		return err
	}
	holdTimeout, err := time.ParseDuration(env("PACT_HOLD_TIMEOUT", "120s"))
	if err != nil {
		return fmt.Errorf("PACT_HOLD_TIMEOUT: %w", err)
	}
	mode := env("PACT_MODE", broker.ModeOn)
	if mode != broker.ModeOn && mode != broker.ModeOff {
		return fmt.Errorf("PACT_MODE must be on or off, got %q", mode)
	}

	model := env("GEMINI_MODEL", "gemini-3.8-flash")
	var fallbacks []string
	fallbackList, set := os.LookupEnv("GEMINI_FALLBACK_MODELS") // set but empty means no fallbacks
	if !set {
		fallbackList = "gemini-3.7-flash,gemini-3.6-flash,gemini-3.5-flash-lite"
	}
	for _, m := range strings.Split(fallbackList, ",") {
		if m = strings.TrimSpace(m); m != "" && m != model {
			fallbacks = append(fallbacks, m)
		}
	}
	exec := executors.New(executors.Config{
		RunnerURL: env("RUNNER_URL", "http://runner:8000"), RunnerToken: os.Getenv("RUNNER_TOKEN"),
		DeployURL: env("DEPLOY_URL", "http://deploy:8000"), SecretsURL: env("SECRETS_URL", "http://secrets:8000"),
		IAMURL: env("IAM_URL", "http://iam:8000"), SinkURL: env("SINK_URL", "http://sink:8000"),
		TargetToken: os.Getenv("TARGET_TOKEN"), EgressPin: os.Getenv("PACT_EGRESS_PIN"),
		GeminiAPIKey: os.Getenv("GEMINI_API_KEY"), GeminiModel: model, GeminiBaseURL: os.Getenv("GEMINI_BASE_URL"),
		GeminiFallbackModels: fallbacks,
	})

	defaults := contract.DefaultsFor(wsName)
	validator := contract.Validator{Workspaces: []string{wsName}, MaxTTL: 24 * time.Hour}
	registry := &contract.Registry{Store: st, Org: ks.Org, Validator: validator}
	compiler := &contract.Compiler{APIKey: os.Getenv("GEMINI_API_KEY"), Model: model, Fallbacks: fallbacks, BaseURL: os.Getenv("GEMINI_BASE_URL"), Defaults: defaults, Validator: validator}

	hub := events.NewHub()
	ev := evidence.New(st, ks.Evidence)
	if data := os.Getenv("PACT_DATA_DIR"); data != "" {
		// Kept on the data volume, apart from the database, so removing records from one cannot hide them.
		ev.WithCheckpoint(filepath.Join(data, "evidence-checkpoint.json"))
	}
	ev.WithCertificate(ks.Org.PublicString(), ks.EvidenceCert)
	ev.OnAppend(func(r evidence.Record) { hub.Publish("decision", r) })

	br := broker.New(broker.Config{Mode: mode, AllowOffMode: os.Getenv("PACT_SCOREBOARD") == "1", HoldTimeout: holdTimeout}, st, ks, registry, dec, ev, exec, ws, hub)
	runner := &scoreboard.Runner{Broker: br, Contracts: registry, Exec: exec, WS: ws, Evidence: ev, Store: st, Hub: hub, Scenarios: scenarios, Defaults: defaults}
	control := &api.Server{Broker: br, Contracts: registry, Compiler: compiler, Evidence: ev, Keys: ks, Exec: exec, WS: ws, Hub: hub,
		Runner: runner, Scenarios: scenarios, Store: st, WebDir: env("PACT_WEB_DIR", "web/out"), StoreName: storeName}

	// Two ports, kept apart: the control plane faces the user, the broker faces the agent.
	brokerAllow, err := netguard.ParseCIDRs(os.Getenv("PACT_BROKER_ALLOW_CIDRS"))
	if err != nil {
		return err
	}
	controlDeny, err := netguard.ParseCIDRs(os.Getenv("PACT_CONTROL_DENY_CIDRS"))
	if err != nil {
		return err
	}
	controlLn, err := net.Listen("tcp", env("PACT_CONTROL_ADDR", ":8080"))
	if err != nil {
		return err
	}
	brokerLn, err := net.Listen("tcp", env("PACT_BROKER_ADDR", ":9090"))
	if err != nil {
		return err
	}
	controlSrv := &http.Server{Handler: control.Handler(), ReadHeaderTimeout: 10 * time.Second}
	brokerSrv := &http.Server{Handler: br.Handler(), ReadHeaderTimeout: 10 * time.Second}

	errc := make(chan error, 2)
	go func() {
		errc <- controlSrv.Serve(netguard.Wrap(controlLn, netguard.Rules{Name: "control-plane", Deny: controlDeny}))
	}()
	go func() {
		errc <- brokerSrv.Serve(netguard.Wrap(brokerLn, netguard.Rules{Name: "broker", Allow: brokerAllow}))
	}()

	log.Printf("control plane on %s, broker on %s", controlLn.Addr(), brokerLn.Addr())
	log.Printf("mode=%s policy=%s store=%s gemini=%v model=%s scenarios=%d", mode, dec.Name(), storeName, exec.LLMConfigured(), model, len(scenarios))
	log.Printf("keys: org=%s issuer=%s evidence=%s (certified by the org key)", ks.Org.Backend, ks.Issuer.Backend, ks.Evidence.Backend)
	log.Printf("evidence public key %s", ks.Evidence.PublicString())
	if mode == broker.ModeOff {
		log.Print("WARNING: PACT_MODE=off. Every tool call executes with no checks.")
	}

	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = controlSrv.Shutdown(shutdown)
	_ = brokerSrv.Shutdown(shutdown)
	return nil
}
