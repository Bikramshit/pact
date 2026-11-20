package userkeys

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

const testKey = "AIzaSyTESTjudgeKey-0123456789abcdefWXYZ"

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newVault(t *testing.T, o Options) (*Vault, *clock) {
	t.Helper()
	v, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	v.now = c.now
	return v, c
}

func TestPutAndUse(t *testing.T) {
	v, _ := newVault(t, Options{})
	sid := NewSessionID()
	info, err := v.Put(sid, testKey, "gemini-x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Last4 != "WXYZ" || info.Status != StatusOK || info.Model != "gemini-x" {
		t.Fatalf("info: %+v", info)
	}
	h := v.Handle(sid)
	if !strings.HasPrefix(h, "uk_") || strings.Contains(h, sid) || strings.Contains(h, testKey[:12]) {
		t.Fatalf("handle reveals something: %q", h)
	}
	key, err := v.KeyFor(h)
	if err != nil || key.Reveal() != testKey {
		t.Fatalf("KeyFor: %v", err)
	}
	if v.Handle(NewSessionID()) != "" {
		t.Fatal("another browser resolved to this browser's key")
	}
}

// The key is held sealed, and the vault never keeps the session id itself.
func TestStoredSealed(t *testing.T) {
	v, _ := newVault(t, Options{})
	sid := NewSessionID()
	_, _ = v.Put(sid, testKey, "")
	dump := fmt.Sprintf("%#v %+v", v.bySID, v.byHandle)
	for _, e := range v.bySID {
		dump += string(e.sealed)
	}
	if strings.Contains(dump, testKey) || strings.Contains(dump, sid) {
		t.Fatal("vault memory holds the key or the session id in the clear")
	}
}

func TestSecretRedacts(t *testing.T) {
	s := NewSecret(testKey)
	for _, out := range []string{fmt.Sprint(s), fmt.Sprintf("%v %s %+v %#v %q", s, s, s, s, s)} {
		if strings.Contains(out, testKey) {
			t.Fatalf("formatted Secret leaks the key: %s", out)
		}
	}
	b, _ := json.Marshal(map[string]any{"k": s})
	if strings.Contains(string(b), testKey) {
		t.Fatalf("JSON leaks the key: %s", b)
	}
	if s.Reveal() != testKey {
		t.Fatal("Reveal lost the key")
	}
}

func TestIdleAndLifetimeExpiry(t *testing.T) {
	v, c := newVault(t, Options{Idle: time.Hour, Lifetime: 3 * time.Hour})
	sid := NewSessionID()
	_, _ = v.Put(sid, testKey, "")
	h := v.Handle(sid)

	c.add(50 * time.Minute)
	if _, err := v.KeyFor(h); err != nil {
		t.Fatal("expired too early")
	}
	c.add(50 * time.Minute) // 100 min after adding, 50 after last use: still inside the sliding idle window
	if _, err := v.KeyFor(h); err != nil {
		t.Fatal("use did not slide the idle timer")
	}
	c.add(61 * time.Minute)
	if _, err := v.KeyFor(h); err != ErrExpired {
		t.Fatalf("idle key still usable: %v", err)
	}
	if _, ok := v.Info(sid); ok {
		t.Fatal("expired key still reported")
	}

	// Kept in use, it still dies at the lifetime.
	_, _ = v.Put(sid, testKey, "")
	h = v.Handle(sid)
	for i := 0; i < 5; i++ {
		c.add(40 * time.Minute)
		_, _ = v.KeyFor(h)
	}
	c.add(time.Minute)
	if _, err := v.KeyFor(h); err != ErrExpired {
		t.Fatalf("key outlived its lifetime: %v", err)
	}
}

func TestRemoveAndReplace(t *testing.T) {
	v, _ := newVault(t, Options{})
	sid := NewSessionID()
	_, _ = v.Put(sid, testKey, "")
	first := v.Handle(sid)
	_, _ = v.Put(sid, strings.Replace(testKey, "WXYZ", "ABCD", 1), "")
	if _, err := v.KeyFor(first); err != ErrExpired {
		t.Fatal("replacing a key left the old one usable")
	}
	if !v.Remove(sid) || v.Handle(sid) != "" || v.Count() != 0 {
		t.Fatal("remove did not delete the key")
	}
}

func TestLimitAndRateLimit(t *testing.T) {
	v, c := newVault(t, Options{Limit: 3})
	var sids []string
	for i := 0; i < 4; i++ {
		sid := NewSessionID()
		sids = append(sids, sid)
		_, _ = v.Put(sid, testKey, "")
		c.add(time.Second)
	}
	if v.Count() != 3 || v.Handle(sids[0]) != "" || v.Handle(sids[3]) == "" {
		t.Fatalf("limit not enforced oldest-first: count=%d", v.Count())
	}
	sid := NewSessionID()
	for i := 0; i < 5; i++ {
		if !v.AllowCheck(sid) {
			t.Fatalf("check %d refused", i+1)
		}
	}
	if v.AllowCheck(sid) {
		t.Fatal("sixth check in ten minutes allowed")
	}
	c.add(11 * time.Minute)
	if !v.AllowCheck(sid) {
		t.Fatal("rate limit did not reset")
	}
}

func TestFormat(t *testing.T) {
	for _, bad := range []string{"", "short", "has space inside the key value!", strings.Repeat("a", 201), "key\nwithnewline1234567890"} {
		if CheckFormat(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if CheckFormat(testKey) != nil {
		t.Error("rejected a well-formed key")
	}
}
