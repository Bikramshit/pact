package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/foxcolab/pact/internal/userkeys"
)

// ClientHeader must be present on every state-changing control-plane request.
//
// Behind Caddy's basic auth, a browser attaches the password to any request
// for the site, including one triggered by another site (a form post, an
// image, a fetch). A custom header cannot be added cross-site without a CORS
// preflight, and pact-core never grants one, so requiring it means only the
// dashboard itself, or a script that sets it on purpose, can change anything:
// add or remove a key, approve a hold, reset the demo.
const ClientHeader = "X-PACT-Client"

// sessionCookie names the cookie that binds a judge's key to their browser.
const sessionCookie = "pact_sid"

// guard applies the request rules every control-plane response needs.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		// The dashboard has buttons that approve actions; it must never be framed by another site.
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/v1/") && !safeMethod(r.Method) {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" || r.Header.Get(ClientHeader) == "" {
				fail(w, http.StatusForbidden, errors.New("refused: state-changing requests must come from the PACT dashboard (missing "+ClientHeader+" header)"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// sessionID returns the browser's session id from its cookie, if it has a well-formed one.
func sessionID(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) != 43 || strings.Trim(c.Value, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
		return ""
	}
	return c.Value
}

// secureRequest reports whether the browser reached us over HTTPS: directly, or
// through Caddy, which sets X-Forwarded-Proto. pact-core's control plane only
// listens on loopback in the deployment, so only Caddy can set that header.
func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func setSession(w http.ResponseWriter, r *http.Request, sid string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sid, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r)})
}

func clearSession(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: secureRequest(r)})
}

// keyRef is the handle of this browser's own key, or "" to use the server key.
func (s *Server) keyRef(r *http.Request) string {
	if s.UserKeys == nil {
		return ""
	}
	return s.UserKeys.Handle(sessionID(r))
}

// userKey unseals this browser's key for a call made right here (contract
// drafting). It returns an empty Secret when the browser has none.
func (s *Server) userKey(r *http.Request) (userkeys.Secret, error) {
	ref := s.keyRef(r)
	if ref == "" {
		return userkeys.Secret{}, nil
	}
	return s.UserKeys.KeyFor(ref)
}

// llmAvailable reports whether this browser can use live features: with its own key, or the server's.
func (s *Server) llmAvailable(r *http.Request) bool {
	return s.Exec.Available(s.keyRef(r))
}

func (s *Server) llmInfo(r *http.Request) obj {
	out := obj{
		"available":         s.llmAvailable(r),
		"source":            "none",
		"server":            s.Exec.ServerKeyStatus(),
		"yours":             nil,
		"user_keys_enabled": s.UserKeys != nil,
	}
	if s.Exec.HasServerKey() {
		out["source"] = "server"
	}
	if s.UserKeys != nil {
		if info, ok := s.UserKeys.Info(sessionID(r)); ok {
			out["source"], out["yours"] = "yours", info
		}
	}
	return out
}

func (s *Server) getUserKey(w http.ResponseWriter, r *http.Request) {
	if s.UserKeys == nil {
		writeJSON(w, 200, obj{"enabled": false, "key": nil})
		return
	}
	info, ok := s.UserKeys.Info(sessionID(r))
	if !ok {
		writeJSON(w, 200, obj{"enabled": true, "key": nil})
		return
	}
	writeJSON(w, 200, obj{"enabled": true, "key": info})
}

var keyProblems = map[string]string{
	userkeys.StatusInvalid:        "Google says this key is not valid. Check that you copied all of it.",
	userkeys.StatusPermission:     "This key is not allowed to use the Gemini API. Enable the Generative Language API for its project, or remove its restrictions.",
	userkeys.StatusQuotaExhausted: "This key works but is out of quota for every model PACT uses. Try another key, or wait for the quota to reset.",
	userkeys.StatusRateLimited:    "Gemini is rate-limiting this key right now. Wait a minute and try again.",
	userkeys.StatusOverloaded:     "Gemini is overloaded right now, so the key could not be checked. Try again in a minute.",
	userkeys.StatusUnreachable:    "The server could not reach Gemini to check the key. Try again in a minute.",
	userkeys.StatusUnknown:        "None of the models PACT uses answered with this key.",
}

// putUserKey checks a key with one tiny Gemini call and, if it works, keeps it
// for this browser. The key is never echoed back, logged or stored on disk.
func (s *Server) putUserKey(w http.ResponseWriter, r *http.Request) {
	if s.UserKeys == nil {
		fail(w, 403, errors.New("adding your own key is turned off on this server"))
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		fail(w, 400, errors.New("request body is not valid JSON"))
		return
	}
	key := strings.TrimSpace(body.Key)
	body.Key = ""
	if err := userkeys.CheckFormat(key); err != nil {
		fail(w, 400, err)
		return
	}
	sid := sessionID(r)
	if sid == "" {
		sid = userkeys.NewSessionID()
	}
	// Set the cookie before checking, so a browser's failed attempts count
	// against its own limit. The id alone grants nothing.
	setSession(w, r, sid)
	if !s.UserKeys.AllowCheck(sid) || !s.allowGlobalCheck() {
		fail(w, 429, errors.New("too many key checks; wait a few minutes and try again"))
		return
	}
	model, status := s.Exec.ValidateKey(r.Context(), key)
	if status != userkeys.StatusOK {
		msg, ok := keyProblems[status]
		if !ok {
			msg = keyProblems[userkeys.StatusUnknown]
		}
		writeJSON(w, 400, obj{"error": msg, "status": status})
		return
	}
	info, err := s.UserKeys.Put(sid, key, model)
	if err != nil {
		fail(w, 400, errors.New(userkeys.Redact(err.Error(), key)))
		return
	}
	writeJSON(w, 200, obj{"key": info})
}

func (s *Server) deleteUserKey(w http.ResponseWriter, r *http.Request) {
	removed := false
	if s.UserKeys != nil {
		removed = s.UserKeys.Remove(sessionID(r))
	}
	clearSession(w, r)
	writeJSON(w, 200, obj{"removed": removed})
}

// allowGlobalCheck caps key checks across all browsers at 30 per 10 minutes, so
// clearing cookies does not turn the server into a key-testing service.
func (s *Server) allowGlobalCheck() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	recent := s.checks[:0]
	for _, t := range s.checks {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	s.checks = recent
	if len(recent) >= 30 {
		return false
	}
	s.checks = append(s.checks, now)
	return true
}
