package canon

import "testing"

func TestCanonicalIsSortedAndCompact(t *testing.T) {
	got, err := Canonicalize([]byte(`{ "b": [1, {"z": true, "a": null}], "a": "x<y>&" }`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":"x<y>&","b":[1,{"a":null,"z":true}]}`
	if string(got) != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSameValueSameBytes(t *testing.T) {
	a, _ := Hash(map[string]any{"x": 1, "y": []string{"a", "b"}})
	b, _ := Hash(map[string]any{"y": []string{"a", "b"}, "x": 1})
	if a != b {
		t.Fatal("equal values produced different hashes")
	}
}

func TestFloatsRejected(t *testing.T) {
	if _, err := Canonicalize([]byte(`{"a": 1.5}`)); err == nil {
		t.Fatal("expected non-integer numbers to be rejected")
	}
}

func TestTrailingDataIsRejected(t *testing.T) {
	for _, in := range []string{`{"a":1}}`, `{"a":1}]`, `{"a":1} {"b":2}`, `{"a":1}x`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse accepted %q", in)
		}
		if _, err := Canonicalize([]byte(in)); err == nil {
			t.Errorf("Canonicalize accepted %q", in)
		}
	}
	if _, err := Parse([]byte(" {\"a\":1} \n")); err != nil {
		t.Errorf("surrounding whitespace is fine: %v", err)
	}
}
