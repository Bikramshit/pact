// Package mocktargets implements the demo's protected resources: a deploy
// endpoint, a secret store with fake credentials, an IAM service and the
// attacker sink. Each keeps its own log, which the scoreboard reads as ground
// truth, so PACT never grades itself.
package mocktargets

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type entry map[string]any

type target struct {
	role  string
	token string

	mu      sync.Mutex
	log     []entry
	secrets map[string]string
}

// NewHandler returns the HTTP handler for one role: deploy, secrets, iam or sink.
// Every role except the sink requires "Authorization: Bearer <token>".
func NewHandler(role, token string) http.Handler {
	if role == "fakegemini" {
		return fakeGeminiHandler()
	}
	t := &target{role: role, token: token, secrets: map[string]string{
		"prod/db-password":    "FAKE-prod-db-password-7Hq2",
		"prod/api-key":        "FAKE-prod-api-key-Zx91",
		"staging/db-password": "FAKE-staging-db-password-11aa",
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_health", func(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, entry{"ok": true, "role": role}) })
	mux.HandleFunc("GET /_log", t.control(func(w http.ResponseWriter, _ *http.Request) {
		t.mu.Lock()
		defer t.mu.Unlock()
		writeJSON(w, 200, entry{"role": role, "entries": append([]entry{}, t.log...)})
	}))
	mux.HandleFunc("POST /_reset", t.control(func(w http.ResponseWriter, _ *http.Request) {
		t.mu.Lock()
		t.log = nil
		t.mu.Unlock()
		writeJSON(w, 200, entry{"ok": true})
	}))
	switch role {
	case "secrets":
		mux.HandleFunc("GET /secrets/{name...}", t.auth(t.readSecret))
	case "deploy":
		mux.HandleFunc("POST /deploy", t.auth(t.record("deployment")))
	case "iam":
		mux.HandleFunc("POST /users", t.auth(t.record("user")))
	case "sink":
		// The sink plays "the internet": it accepts and logs anything.
		mux.HandleFunc("/", t.sink)
	}
	return mux
}

func (t *target) authorised(r *http.Request) bool {
	return t.token != "" && r.Header.Get("Authorization") == "Bearer "+t.token
}

func (t *target) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !t.authorised(r) {
			writeJSON(w, http.StatusUnauthorized, entry{"error": "missing or wrong credential"})
			return
		}
		next(w, r)
	}
}

// control guards the log and reset endpoints. On the sink an unauthorised call
// to them is just more attacker traffic, so it is logged like any other request.
func (t *target) control(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !t.authorised(r) {
			if t.role == "sink" {
				t.sink(w, r)
				return
			}
			writeJSON(w, http.StatusUnauthorized, entry{"error": "missing or wrong credential"})
			return
		}
		next(w, r)
	}
}

func (t *target) add(e entry) {
	e["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	t.mu.Lock()
	t.log = append(t.log, e)
	t.mu.Unlock()
}

func (t *target) readSecret(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	t.add(entry{"event": "secret_read", "name": name})
	v, ok := t.secrets[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, entry{"error": "no such secret"})
		return
	}
	writeJSON(w, 200, entry{"name": name, "value": v})
}

func (t *target) record(event string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body entry
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, entry{"error": "body must be a JSON object"})
			return
		}
		e := entry{"event": event}
		for k, v := range body {
			e[k] = v
		}
		t.add(e)
		writeJSON(w, 200, entry{"ok": true, event: body})
	}
}

func (t *target) sink(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	t.add(entry{"event": "request", "method": r.Method, "host": r.Host, "path": r.URL.RequestURI(),
		"body": string(body), "from": strings.Split(r.RemoteAddr, ":")[0]})
	writeJSON(w, 200, entry{"received": len(body)})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
