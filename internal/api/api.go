// Package api is the control plane: the user-facing API and dashboard on port
// 8080. It is bound to the host and refuses connections from the agent's network.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/foxcolab/pact/internal/broker"
	"github.com/foxcolab/pact/internal/broker/executors"
	"github.com/foxcolab/pact/internal/contract"
	"github.com/foxcolab/pact/internal/events"
	"github.com/foxcolab/pact/internal/evidence"
	"github.com/foxcolab/pact/internal/keys"
	"github.com/foxcolab/pact/internal/scenario"
	"github.com/foxcolab/pact/internal/scoreboard"
	"github.com/foxcolab/pact/internal/store"
	"github.com/foxcolab/pact/internal/userkeys"
	"github.com/foxcolab/pact/internal/workspace"
)

// Server holds everything the control plane needs.
type Server struct {
	Broker    *broker.Broker
	Contracts *contract.Registry
	Compiler  *contract.Compiler
	Evidence  *evidence.Engine
	Keys      *keys.Set
	Exec      *executors.Targets
	WS        *workspace.Workspace
	Hub       *events.Hub
	Runner    *scoreboard.Runner
	Scenarios []scenario.Scenario
	Store     store.Store
	WebDir    string
	StoreName string
	// UserKeys holds Gemini keys judges add in their own browsers. Nil turns the
	// feature off (PACT_USER_KEYS=off).
	UserKeys *userkeys.Vault

	mu     sync.Mutex
	drafts map[string]contract.Draft
	checks []time.Time // recent key checks across all browsers, for a global limit
	// clientChecks are recent key checks per client address.
	clientChecks map[string][]time.Time
}

// Handler returns the control-plane routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, obj{"ok": true}) })
	mux.HandleFunc("GET /v1/status", s.status)
	mux.HandleFunc("GET /v1/keys", s.publicKeys)
	mux.HandleFunc("GET /v1/session/key", s.getUserKey)
	mux.HandleFunc("POST /v1/session/key", s.putUserKey)
	mux.HandleFunc("DELETE /v1/session/key", s.deleteUserKey)

	mux.HandleFunc("POST /v1/contracts/draft", s.draft)
	mux.HandleFunc("POST /v1/contracts/{id}/confirm", s.confirm)
	mux.HandleFunc("GET /v1/contracts", s.listContracts)
	mux.HandleFunc("GET /v1/contracts/{id}", s.getContract)

	mux.HandleFunc("POST /v1/agents", s.registerAgent)
	mux.HandleFunc("GET /v1/agents", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, obj{"agents": s.Broker.Agents.List()}) })

	mux.HandleFunc("POST /v1/capabilities", s.mint)
	mux.HandleFunc("POST /v1/capabilities/{id}/revoke", s.revoke)

	mux.HandleFunc("POST /v1/tasks", s.startTask)
	mux.HandleFunc("GET /v1/tasks", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, obj{"tasks": s.Broker.Agents.Assignments()})
	})
	mux.HandleFunc("GET /v1/sessions", s.listSessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)

	mux.HandleFunc("GET /v1/decisions/stream", s.stream)
	mux.HandleFunc("GET /v1/evidence", s.listEvidence)
	mux.HandleFunc("GET /v1/evidence/verify", s.verifyEvidence)
	mux.HandleFunc("GET /v1/evidence/export", s.exportEvidence)
	mux.HandleFunc("GET /v1/stats", s.stats)

	mux.HandleFunc("GET /v1/holds", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, obj{"holds": s.Broker.Holds.List()}) })
	mux.HandleFunc("POST /v1/holds/{id}/approve", s.resolveHold("approved"))
	mux.HandleFunc("POST /v1/holds/{id}/reject", s.resolveHold("rejected"))

	mux.HandleFunc("GET /v1/scenarios", s.listScenarios)
	mux.HandleFunc("GET /v1/oracles", s.oracles)
	mux.HandleFunc("POST /v1/demo/reset", s.demoReset)
	mux.HandleFunc("POST /v1/replay", s.replay)
	mux.HandleFunc("POST /v1/scoreboard/runs", s.startScoreboard)
	mux.HandleFunc("GET /v1/scoreboard/latest", s.latestScoreboard)
	mux.HandleFunc("GET /v1/scoreboard/runs", s.scoreboardHistory)
	mux.HandleFunc("GET /v1/scoreboard/runs/{id}", s.scoreboardRun)
	mux.HandleFunc("GET /v1/redteam", s.redteamInfo)
	mux.HandleFunc("POST /v1/redteam", s.startRedteam)

	mux.Handle("/", s.static())
	return guard(mux)
}

type obj = map[string]any

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, obj{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return errors.New("request body is not valid JSON")
	}
	return nil
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	online := 0
	for _, a := range s.Broker.Agents.List() {
		if a.Online {
			online++
		}
	}
	writeJSON(w, 200, obj{
		"mode":              s.Broker.Mode(),
		"policy_engine":     s.Broker.PolicyEngine(),
		"gemini_configured": s.llmAvailable(r),
		"llm":               s.llmInfo(r),
		"model":             s.Exec.Model(),
		"llm_backend":       s.Exec.LLMBackend(),
		"fallback_models":   s.Exec.FallbackModels(),
		"key_backend":       s.Keys.Org.Backend,
		"models_used":       s.Exec.ModelUsage(),
		"agents_online":     online,
		"workspace":         s.WS.Name,
		"store":             s.StoreName,
		"time":              time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) publicKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, obj{"org": s.Keys.Org.PublicString(), "issuer": s.Keys.Issuer.PublicString(), "evidence": s.Keys.Evidence.PublicString(),
		"backends":             obj{"org": s.Keys.Org.Backend, "issuer": s.Keys.Issuer.Backend, "evidence": s.Keys.Evidence.Backend},
		"evidence_certificate": s.Keys.EvidenceCert,
		"evidence_certified":   keys.VerifyEvidenceCert(s.Keys.Org.Public, s.Keys.Evidence.PublicString(), s.Keys.EvidenceCert)})
}

// --- contracts ---

func (s *Server) draft(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Instruction string `json:"instruction"`
		Template    bool   `json:"template"` // skip Gemini and load the pre-written contract
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if strings.TrimSpace(body.Instruction) == "" {
		fail(w, 400, errors.New("instruction is required"))
		return
	}
	if utf8.RuneCountInString(body.Instruction) > maxInstruction {
		fail(w, 400, fmt.Errorf("instruction is limited to %d characters", maxInstruction))
		return
	}
	var d contract.Draft
	if body.Template {
		t := contract.Template(strings.TrimSpace(body.Instruction), s.Compiler.Defaults, time.Now())
		d = contract.Draft{Contract: t, Summary: contract.Describe(t), Ambiguities: []string{}, DraftedBy: "template", Warnings: []string{}}
	} else {
		apiKey, err := s.userKey(r)
		if err != nil {
			fail(w, 409, err)
			return
		}
		d = s.Compiler.Draft(r.Context(), body.Instruction, apiKey.Reveal())
	}
	s.mu.Lock()
	if s.drafts == nil {
		s.drafts = map[string]contract.Draft{}
	}
	// A draft can only be signed before its contract expires; drop the rest.
	now := time.Now()
	for id, old := range s.drafts {
		if old.Contract.Expired(now) {
			delete(s.drafts, id)
		}
	}
	s.drafts[d.Contract.ContractID] = d
	s.mu.Unlock()
	writeJSON(w, 200, d)
}

// maxInstruction bounds a task instruction; drafts are kept in memory until they expire.
const maxInstruction = 4000

func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		ConfirmedBy string             `json:"confirmed_by"`
		AgentID     string             `json:"agent_id"`
		Contract    *contract.Contract `json:"contract"` // optional: the draft as edited by the user
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	s.mu.Lock()
	d, ok := s.drafts[id]
	s.mu.Unlock()
	if !ok {
		fail(w, 404, errors.New("no such draft; draft the contract again"))
		return
	}
	c := d.Contract
	if body.Contract != nil {
		edited := *body.Contract
		// Identity and lifetime come from the server-side draft, never from the client.
		edited.ContractID, edited.TaskID, edited.Version, edited.ExpiresAt = c.ContractID, c.TaskID, c.Version, c.ExpiresAt
		edited.Instruction = c.Instruction
		// The signer may tighten the trust labels, never loosen the organisation's.
		contract.KeepOrgLabels(&edited, s.Compiler.Defaults)
		c = edited
	}
	if body.AgentID == "" {
		for _, a := range s.Broker.Agents.List() {
			if a.Online {
				body.AgentID = a.AgentID
				break
			}
		}
	}
	if _, ok := s.Broker.Agents.Get(body.AgentID); !ok {
		fail(w, 409, errors.New("no registered agent to bind the contract to; start the agent container first"))
		return
	}
	c.AgentID = body.AgentID
	signed, err := s.Contracts.Confirm(r.Context(), c, body.ConfirmedBy)
	if err != nil {
		fail(w, 400, err)
		return
	}
	s.mu.Lock()
	delete(s.drafts, id)
	s.mu.Unlock()
	s.Hub.Publish("contract", signed)
	writeJSON(w, 200, obj{"contract": signed, "summary": contract.Describe(signed)})
}

func (s *Server) listContracts(w http.ResponseWriter, r *http.Request) {
	list, err := s.Contracts.List(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, obj{"contracts": list})
}

func (s *Server) getContract(w http.ResponseWriter, r *http.Request) {
	c, err := s.Contracts.ByID(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, notFoundOr(err, 500), err)
		return
	}
	writeJSON(w, 200, obj{"contract": c, "summary": contract.Describe(c), "verified": true})
}

func notFoundOr(err error, other int) int {
	if errors.Is(err, store.ErrNotFound) {
		return 404
	}
	return other
}

// --- agents and capabilities ---

func (s *Server) registerAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Pubkey string `json:"pubkey"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	ag, err := s.Broker.RegisterAgent(r.Context(), body.Pubkey)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, ag)
}

func (s *Server) mint(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ContractID string `json:"contract_id"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	c, err := s.Contracts.ByID(r.Context(), body.ContractID)
	if err != nil {
		fail(w, notFoundOr(err, 400), err)
		return
	}
	rec, err := s.Broker.MintCapability(r.Context(), c, broker.MintOptions{})
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, obj{"cap_id": rec.CapID, "token": rec.Token, "payload": rec.Payload})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	if err := s.Broker.RevokeCapability(r.Context(), r.PathValue("id")); err != nil {
		fail(w, notFoundOr(err, 500), err)
		return
	}
	writeJSON(w, 200, obj{"revoked": r.PathValue("id")})
}

// --- tasks ---

func (s *Server) startTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ContractID string `json:"contract_id"`
		Kind       string `json:"kind"`     // live | replay
		Scenario   string `json:"scenario"` // repository state to load; also the script for replay
		Variant    string `json:"variant"`
		Reset      bool   `json:"reset"`
		MaxSteps   int    `json:"max_steps"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if body.Kind == "" {
		body.Kind = "live"
	}
	if body.Kind != "live" && body.Kind != "replay" {
		fail(w, 400, errors.New("kind must be live or replay"))
		return
	}
	c, err := s.Contracts.ByID(r.Context(), body.ContractID)
	if err != nil {
		fail(w, notFoundOr(err, 400), err)
		return
	}
	var sc scenario.Scenario
	var overlay map[string]string
	if body.Scenario != "" {
		found, ok := scenario.Find(s.Scenarios, body.Scenario)
		if !ok {
			fail(w, 400, fmt.Errorf("unknown scenario %q", body.Scenario))
			return
		}
		v, ok := found.Variant(body.Variant)
		if !ok {
			fail(w, 400, fmt.Errorf("scenario %s has no variant %q", found.ID, body.Variant))
			return
		}
		sc, overlay = found, v.Overlay
	}
	if body.Kind == "replay" && len(sc.Replay) == 0 {
		fail(w, 400, errors.New("replay needs a scenario with a script"))
		return
	}
	if body.Kind == "live" && !s.llmAvailable(r) {
		fail(w, 409, errors.New("no working Gemini key: add your own key, or use the scripted agent"))
		return
	}
	if body.MaxSteps < 0 || body.MaxSteps > 100 {
		fail(w, 400, errors.New("max_steps must be between 0 (the default) and 100"))
		return
	}
	// Reset and hand-over happen together, and never while a scoreboard,
	// replay or Break it run is measuring the same repository and targets.
	var rec broker.CapabilityRecord
	var as *broker.Assignment
	err = s.Runner.Exclusive(func() error {
		if body.Reset {
			if err := s.Runner.Reset(r.Context(), overlay); err != nil {
				return err
			}
		}
		var err error
		if rec, err = s.Broker.MintCapability(r.Context(), c, broker.MintOptions{KeyRef: s.keyRef(r)}); err != nil {
			return badRequest{err}
		}
		as = &broker.Assignment{TaskID: c.TaskID, AgentID: c.AgentID, CapID: rec.CapID, Capability: rec.Token, Kind: body.Kind,
			Instruction: c.Instruction, MaxSteps: body.MaxSteps, Label: c.TaskID}
		if body.Kind == "replay" {
			as.Steps = sc.Replay
		}
		s.Broker.Agents.Enqueue(as)
		return nil
	})
	if err != nil {
		fail(w, worldStatus(err), err)
		return
	}
	writeJSON(w, 200, obj{"task_id": c.TaskID, "cap_id": rec.CapID, "assignment_id": as.ID, "kind": body.Kind})
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Broker.Sessions(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, obj{"sessions": list})
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	sess, err := s.Broker.Session(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, notFoundOr(err, 500), err)
		return
	}
	writeJSON(w, 200, sess)
}

// --- live stream ---

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, errors.New("streaming is not supported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Hub.Subscribe()
	defer cancel()
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case ev := <-ch:
			data, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		}
		flusher.Flush()
	}
}

// --- evidence ---

// listEvidence returns records in sequence order. With limit it returns the
// newest limit records, or limit records starting at from_seq; total is always
// the full count so a viewer can say what it is not showing.
func (s *Server) listEvidence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	recs, err := s.Evidence.List(r.Context(), q.Get("task_id"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	total := len(recs)
	limit, _ := strconv.Atoi(q.Get("limit"))
	fromSeq, _ := strconv.ParseInt(q.Get("from_seq"), 10, 64)
	if fromSeq > 0 {
		i := 0
		for i < len(recs) && recs[i].Seq < fromSeq {
			i++
		}
		recs = recs[i:]
		if limit > 0 && len(recs) > limit {
			recs = recs[:limit]
		}
	} else if limit > 0 && len(recs) > limit {
		recs = recs[len(recs)-limit:]
	}
	writeJSON(w, 200, obj{"records": recs, "total": total})
}

// stats summarises the decision log for the overview page.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	recs, err := s.Evidence.List(r.Context(), "")
	if err != nil {
		fail(w, 500, err)
		return
	}
	type statsOut struct {
		Records    int    `json:"records"`
		Allowed    int    `json:"allowed"`
		Denied     int    `json:"denied"`
		Held       int    `json:"held"`     // holds opened
		Approved   int    `json:"approved"` // holds a human approved
		Rejected   int    `json:"rejected"` // holds rejected or timed out
		ModelCalls int    `json:"model_calls"`
		TaintReads int    `json:"taint_reads"`
		Tasks      int    `json:"tasks"`
		LastAt     string `json:"last_at,omitempty"`
	}
	out := statsOut{}
	tasks := map[string]bool{}
	for _, rec := range recs {
		out.Records++
		if rec.TaskID != "" {
			tasks[rec.TaskID] = true
		}
		if rec.Request.Tool == "llm_generate" {
			out.ModelCalls++
		}
		if rec.TaintAdded != nil {
			out.TaintReads++
		}
		switch {
		case rec.Decision == "allow":
			out.Allowed++
		case rec.Decision == "deny":
			out.Denied++
		case rec.Decision == "hold" && rec.Approval == nil:
			out.Held++
		case rec.Decision == "hold" && rec.Approval.Result == "approved":
			out.Approved++
		case rec.Decision == "hold":
			out.Rejected++
		}
		out.LastAt = rec.TS
	}
	out.Tasks = len(tasks)
	writeJSON(w, 200, out)
}

func (s *Server) verifyEvidence(w http.ResponseWriter, r *http.Request) {
	res, err := s.Evidence.Verify(r.Context())
	if err != nil {
		// Fail closed: an unreadable log is not a verified log.
		writeJSON(w, 500, obj{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) exportEvidence(w http.ResponseWriter, r *http.Request) {
	ex, err := s.Evidence.Export(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="pact-evidence.json"`)
	writeJSON(w, 200, ex)
}

// --- holds ---

func (s *Server) resolveHold(result string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			By string `json:"by"`
		}
		if err := readJSON(r, &body); err != nil {
			fail(w, 400, err)
			return
		}
		if strings.TrimSpace(body.By) == "" {
			fail(w, 400, errors.New("by is required: the approver is written to the evidence chain"))
			return
		}
		if err := s.Broker.Holds.Resolve(r.PathValue("id"), result, strings.TrimSpace(body.By)); err != nil {
			fail(w, 409, err)
			return
		}
		writeJSON(w, 200, obj{"hold_id": r.PathValue("id"), "result": result})
	}
}

// --- demo, replay, scoreboard ---

func (s *Server) listScenarios(w http.ResponseWriter, _ *http.Request) {
	type item struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Kind        string   `json:"kind"`
		Description string   `json:"description"`
		Instruction string   `json:"instruction"`
		Variants    []string `json:"variants"`
		Steps       int      `json:"steps"`
		Scoreboard  bool     `json:"scoreboard"`
	}
	out := []item{}
	for _, sc := range s.Scenarios {
		it := item{ID: sc.ID, Title: sc.Title, Kind: sc.Kind, Description: sc.Description, Instruction: sc.Instruction, Steps: len(sc.Replay), Scoreboard: sc.Scoreboard}
		for _, v := range sc.Variants {
			it.Variants = append(it.Variants, v.ID)
		}
		out = append(out, it)
	}
	writeJSON(w, 200, obj{"scenarios": out})
}

func (s *Server) oracles(w http.ResponseWriter, r *http.Request) {
	obs, err := scoreboard.Observe(r.Context(), s.Exec, s.WS)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, obj{"observation": obs, "effects": obs.Effects(), "completed": obs.Completed()})
}

func (s *Server) demoReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scenario string `json:"scenario"`
		Variant  string `json:"variant"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	var overlay map[string]string
	if body.Scenario != "" {
		sc, ok := scenario.Find(s.Scenarios, body.Scenario)
		if !ok {
			fail(w, 400, fmt.Errorf("unknown scenario %q", body.Scenario))
			return
		}
		v, ok := sc.Variant(body.Variant)
		if !ok {
			fail(w, 400, fmt.Errorf("scenario %s has no variant %q", sc.ID, body.Variant))
			return
		}
		overlay = v.Overlay
	}
	if err := s.Runner.Exclusive(func() error { return s.Runner.Reset(r.Context(), overlay) }); err != nil {
		fail(w, worldStatus(err), err)
		return
	}
	writeJSON(w, 200, obj{"ok": true, "scenario": body.Scenario})
}

// replay runs scripted scenarios with PACT on and checks every step against its
// expected decision. It is the acceptance test, runnable against the live stack.
func (s *Server) replay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Scenarios []string `json:"scenarios"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if len(body.Scenarios) == 0 {
		for _, sc := range s.Scenarios {
			if len(sc.Replay) > 0 {
				body.Scenarios = append(body.Scenarios, sc.ID)
			}
		}
	}
	ctx := context.WithoutCancel(r.Context())
	opts := make([]scoreboard.Options, len(body.Scenarios))
	for i, id := range body.Scenarios {
		opts[i] = scoreboard.Options{Scenario: id, Mode: broker.ModeOn, AgentKind: "replay", AutoRejectHolds: true,
			Timeout: 90 * time.Second, ConfirmedBy: "replay"}
	}
	results, errs, err := s.Runner.Batch(ctx, opts)
	if err != nil {
		fail(w, worldStatus(err), err)
		return
	}
	allOK := true
	for i, res := range results {
		sc, found := scenario.Find(s.Scenarios, body.Scenarios[i])
		if errs[i] != nil || !res.StepsOK || len(res.Unauthorized) > 0 || res.AgentStatus != "done" || (found && res.Completed != sc.ExpectCompletion) {
			allOK = false
		}
	}
	writeJSON(w, 200, obj{"ok": allOK, "results": results})
}

// badRequest marks an error inside an exclusive section as the caller's fault.
type badRequest struct{ error }

// worldStatus maps errors from a reset or hand-over to a status code.
func worldStatus(err error) int {
	var bad badRequest
	switch {
	case errors.Is(err, scoreboard.ErrBusy):
		return 409
	case errors.As(err, &bad):
		return 400
	}
	return 500
}

func (s *Server) startScoreboard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Agent      string `json:"agent"` // replay | live
		Quick      bool   `json:"quick"`
		Supervisor bool   `json:"supervisor"` // add the AI-supervisor baseline
		MaxSteps   int    `json:"max_steps"`
		Timeout    int    `json:"timeout_seconds"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if body.Agent == "" {
		body.Agent = "replay"
	}
	if body.Agent != "replay" && body.Agent != "live" {
		fail(w, 400, errors.New("agent must be replay or live"))
		return
	}
	if body.MaxSteps < 0 || body.MaxSteps > 100 || body.Timeout < 0 || body.Timeout > 1800 {
		fail(w, 400, errors.New("max_steps must be 0 to 100 and timeout_seconds 0 to 1800 (0 means the default)"))
		return
	}
	if body.Agent == "live" && !s.llmAvailable(r) {
		fail(w, 409, errors.New("no working Gemini key: add your own key, or use the scripted agent"))
		return
	}
	if body.Supervisor && !s.llmAvailable(r) {
		fail(w, 409, errors.New("the AI-supervisor baseline needs a Gemini key: add your own key"))
		return
	}
	run, err := s.Runner.Start(scoreboard.RunOptions{AgentKind: body.Agent, Quick: body.Quick, Supervisor: body.Supervisor, MaxSteps: body.MaxSteps,
		Timeout: time.Duration(body.Timeout) * time.Second, KeyRef: s.keyRef(r)})
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 202, run)
}

func (s *Server) latestScoreboard(w http.ResponseWriter, r *http.Request) {
	run, ok := s.Runner.Latest(r.Context())
	if !ok {
		writeJSON(w, 200, obj{"run": nil})
		return
	}
	writeJSON(w, 200, obj{"run": run})
}

func (s *Server) scoreboardHistory(w http.ResponseWriter, r *http.Request) {
	runs, err := s.Runner.History(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, obj{"runs": runs})
}

func (s *Server) scoreboardRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.Runner.ByID(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, notFoundOr(err, 500), err)
		return
	}
	writeJSON(w, 200, obj{"run": run})
}

// redteamInfo returns the README a red-team run starts from and the latest run, if any.
func (s *Server) redteamInfo(w http.ResponseWriter, _ *http.Request) {
	out := obj{"base_readme": s.Runner.BenignReadme(), "path": scoreboard.ReadmePath, "run": nil,
		"instructions": obj{"guarded": scenario.DefaultInstruction, "plain": scoreboard.PlainInstruction}}
	if run, ok := s.Runner.LatestRedteam(); ok {
		out["run"] = run
	}
	writeJSON(w, 200, out)
}

// startRedteam plants caller-written text in the dependency README and runs the
// live agent against it without and with PACT. The workspace is disposable and
// demo egress is pinned to the sink, so the text can say anything.
func (s *Server) startRedteam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Injection   string `json:"injection"`
		Supervisor  bool   `json:"supervisor"`
		Interactive bool   `json:"interactive"`
		Plain       bool   `json:"plain"` // task without the "do not access secrets" warning
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	body.Injection = strings.TrimSpace(body.Injection)
	if body.Injection == "" {
		fail(w, 400, errors.New("injection is required: write the text you want the agent to find"))
		return
	}
	if utf8.RuneCountInString(body.Injection) > 8000 {
		fail(w, 400, errors.New("injection is limited to 8000 characters"))
		return
	}
	if !s.llmAvailable(r) {
		fail(w, 409, errors.New("no working Gemini key: a red-team run needs the live agent, so add your own key"))
		return
	}
	readme := strings.TrimRight(s.Runner.BenignReadme(), "\n") + "\n\n" + body.Injection + "\n"
	run, err := s.Runner.StartRedteam(scoreboard.RedteamOptions{Readme: readme, Supervisor: body.Supervisor, Interactive: body.Interactive, Plain: body.Plain, KeyRef: s.keyRef(r)})
	if err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 202, obj{"run": run})
}

// --- dashboard ---

func (s *Server) static() http.Handler {
	if _, err := os.Stat(filepath.Join(s.WebDir, "index.html")); err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><title>PACT</title><body style="font-family:system-ui;margin:3rem">
<h1>PACT control plane</h1><p>The dashboard has not been built. The API is available under <code>/v1</code>;
try <a href="/v1/status">/v1/status</a> and <a href="/v1/evidence/verify">/v1/evidence/verify</a>.</p></body>`)
		})
	}
	files := http.FileServer(http.Dir(s.WebDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			fail(w, 404, errors.New("no such endpoint"))
			return
		}
		files.ServeHTTP(w, r)
	})
}
