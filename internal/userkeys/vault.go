// Package userkeys holds Gemini API keys that judges paste into the dashboard.
//
// A key lives only in this process's memory, sealed with AES-256-GCM under a
// key generated at start, and only for a short time. It is bound to one browser
// by a random session id the browser holds in an HttpOnly cookie; the vault
// stores only the SHA-256 of that id. Everything outside this package (capability
// records, sessions, scoreboard runs) refers to a key by a random handle, which
// reveals nothing about the key or the browser.
package userkeys

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Health of a key, as last observed.
const (
	StatusOK             = "ok"
	StatusQuotaExhausted = "quota_exhausted"
	StatusInvalid        = "invalid"
	StatusPermission     = "permission"
	StatusOverloaded     = "overloaded"
	StatusRateLimited    = "rate_limited"
	StatusUnreachable    = "unreachable"
	StatusMissing        = "missing"
	StatusUnknown        = "unknown"
)

// ErrExpired is returned when a handle no longer resolves to a key.
var ErrExpired = errors.New("your Gemini key has expired or was removed; add it again")

var keyFormat = regexp.MustCompile(`^[A-Za-z0-9_\-.]{20,200}$`)

// CheckFormat rejects anything that cannot be an API key before it is sent anywhere.
func CheckFormat(key string) error {
	if !keyFormat.MatchString(key) {
		return errors.New("that does not look like an API key: expected 20 to 200 letters, digits, '-', '_' or '.'")
	}
	return nil
}

// Secret carries an API key between the vault and the HTTP request that uses it.
// It prints and marshals as "[redacted]", so an accidental log line or JSON
// encoding cannot leak it. Call Reveal only at the point of use.
type Secret struct{ v string }

// NewSecret wraps a key.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the key itself.
func (s Secret) Reveal() string { return s.v }

// Empty reports whether no key is held.
func (s Secret) Empty() bool { return s.v == "" }

func (s Secret) String() string               { return "[redacted]" }
func (s Secret) GoString() string             { return "userkeys.Secret{[redacted]}" }
func (s Secret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted]"`), nil }
func (s Secret) MarshalText() ([]byte, error) { return []byte("[redacted]"), nil }

// Info is what the dashboard may know about a stored key. It never includes the key.
type Info struct {
	Last4     string    `json:"last4"`
	AddedAt   time.Time `json:"added_at"`
	LastUsed  time.Time `json:"last_used"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"`
	Model     string    `json:"model,omitempty"` // the model that answered the check
}

type entry struct {
	handle   string
	nonce    []byte
	sealed   []byte
	last4    string
	addedAt  time.Time
	lastUsed time.Time
	status   string
	model    string
	checks   []time.Time // recent validation attempts, for rate limiting
}

// Vault is the in-memory store. The zero value is not usable; call New.
type Vault struct {
	aead     cipher.AEAD
	idle     time.Duration
	lifetime time.Duration
	limit    int
	now      func() time.Time

	mu       sync.Mutex
	bySID    map[[32]byte]*entry
	byHandle map[string]*entry
	pending  map[[32]byte][]time.Time // validation attempts from browsers with no key yet
}

// Options tunes a vault.
type Options struct {
	Idle     time.Duration // removed after this long unused (default 2h)
	Lifetime time.Duration // removed this long after being added, used or not (default 12h)
	Limit    int           // most keys held at once (default 200)
}

// New returns an empty vault with a fresh sealing key.
func New(o Options) (*Vault, error) {
	if o.Idle <= 0 {
		o.Idle = 2 * time.Hour
	}
	if o.Lifetime <= 0 {
		o.Lifetime = 12 * time.Hour
	}
	if o.Limit <= 0 {
		o.Limit = 200
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead, idle: o.Idle, lifetime: o.Lifetime, limit: o.Limit, now: time.Now,
		bySID: map[[32]byte]*entry{}, byHandle: map[string]*entry{}, pending: map[[32]byte][]time.Time{}}, nil
}

// NewSessionID returns a random, URL-safe session id for a browser cookie.
func NewSessionID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func sidHash(sid string) [32]byte { return sha256.Sum256([]byte("pact-session:" + sid)) }

func newHandle() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "uk_" + hex.EncodeToString(b)
}

func (v *Vault) expiry(e *entry) time.Time {
	idle := e.lastUsed.Add(v.idle)
	life := e.addedAt.Add(v.lifetime)
	if idle.Before(life) {
		return idle
	}
	return life
}

func (v *Vault) info(e *entry) Info {
	return Info{Last4: e.last4, AddedAt: e.addedAt, LastUsed: e.lastUsed, ExpiresAt: v.expiry(e), Status: e.status, Model: e.model}
}

// live returns the entry for a session if it exists and has not expired. Caller holds mu.
func (v *Vault) live(sid string) *entry {
	if sid == "" {
		return nil
	}
	h := sidHash(sid)
	e, ok := v.bySID[h]
	if !ok {
		return nil
	}
	if !v.now().Before(v.expiry(e)) {
		v.dropLocked(h, e)
		return nil
	}
	return e
}

func (v *Vault) dropLocked(h [32]byte, e *entry) {
	for i := range e.sealed {
		e.sealed[i] = 0
	}
	delete(v.bySID, h)
	delete(v.byHandle, e.handle)
}

// AllowCheck records a validation attempt for a browser and reports whether it
// is within the limit: 5 attempts per 10 minutes.
func (v *Vault) AllowCheck(sid string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	h := sidHash(sid)
	now := v.now()
	recent := v.pending[h][:0]
	for _, t := range v.pending[h] {
		if now.Sub(t) < 10*time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= 5 {
		v.pending[h] = recent
		return false
	}
	v.pending[h] = append(recent, now)
	return true
}

// Put stores key for the browser holding sid, replacing any earlier key, and
// returns what the dashboard may see. model is the model that answered the check.
func (v *Vault) Put(sid, key, model string) (Info, error) {
	if sid == "" {
		return Info{}, errors.New("no session")
	}
	if err := CheckFormat(key); err != nil {
		return Info{}, err
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return Info{}, err
	}
	now := v.now()
	e := &entry{handle: newHandle(), nonce: nonce, last4: key[len(key)-4:], addedAt: now, lastUsed: now, status: StatusOK, model: model}
	e.sealed = v.aead.Seal(nil, nonce, []byte(key), []byte(e.handle))

	v.mu.Lock()
	defer v.mu.Unlock()
	h := sidHash(sid)
	if old, ok := v.bySID[h]; ok {
		v.dropLocked(h, old)
	}
	v.sweepLocked()
	if len(v.bySID) >= v.limit {
		v.evictOldestLocked()
	}
	v.bySID[h] = e
	v.byHandle[e.handle] = e
	return v.info(e), nil
}

// Info returns what the dashboard may know about this browser's key, if any.
func (v *Vault) Info(sid string) (Info, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e := v.live(sid)
	if e == nil {
		return Info{}, false
	}
	return v.info(e), true
}

// Handle returns the opaque handle for this browser's key, or "" if it has none.
// Runs record the handle, never the key.
func (v *Vault) Handle(sid string) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e := v.live(sid); e != nil {
		return e.handle
	}
	return ""
}

// Remove deletes this browser's key. It reports whether there was one.
func (v *Vault) Remove(sid string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	h := sidHash(sid)
	e, ok := v.bySID[h]
	if ok {
		v.dropLocked(h, e)
	}
	delete(v.pending, h)
	return ok
}

// KeyFor unseals the key behind a handle and restarts its idle timer. Only the
// executors that call Gemini, and contract drafting, should call it.
func (v *Vault) KeyFor(handle string) (Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	e, ok := v.byHandle[handle]
	if !ok {
		return Secret{}, ErrExpired
	}
	now := v.now()
	if !now.Before(v.expiry(e)) {
		v.dropLocked(sidOf(v, e), e)
		return Secret{}, ErrExpired
	}
	plain, err := v.aead.Open(nil, e.nonce, e.sealed, []byte(e.handle))
	if err != nil {
		return Secret{}, errors.New("userkeys: stored key could not be unsealed")
	}
	e.lastUsed = now
	return Secret{v: string(plain)}, nil
}

func sidOf(v *Vault, target *entry) [32]byte {
	for h, e := range v.bySID {
		if e == target {
			return h
		}
	}
	return [32]byte{}
}

// Report records the health of a key as observed on a real call.
func (v *Vault) Report(handle, status string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if e, ok := v.byHandle[handle]; ok && status != "" {
		e.status = status
	}
}

// Sweep removes expired keys and stale rate-limit records.
func (v *Vault) Sweep() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sweepLocked()
}

func (v *Vault) sweepLocked() {
	now := v.now()
	for h, e := range v.bySID {
		if !now.Before(v.expiry(e)) {
			v.dropLocked(h, e)
		}
	}
	for h, ts := range v.pending {
		if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= 10*time.Minute {
			delete(v.pending, h)
		}
	}
}

func (v *Vault) evictOldestLocked() {
	type item struct {
		h [32]byte
		e *entry
	}
	items := make([]item, 0, len(v.bySID))
	for h, e := range v.bySID {
		items = append(items, item{h, e})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].e.lastUsed.Before(items[j].e.lastUsed) })
	for _, it := range items[:len(items)-v.limit+1] {
		v.dropLocked(it.h, it.e)
	}
}

// Count reports how many keys are held, for status pages.
func (v *Vault) Count() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.bySID)
}

// Redact replaces any occurrence of key in s, for error messages that might echo input.
func Redact(s, key string) string {
	if key == "" {
		return s
	}
	return strings.ReplaceAll(s, key, "[redacted]")
}
