package cryptobox

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	box := newTestBox(t)
	plain := []byte("super-secret-webhook-key")
	ct, err := box.Seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(ct, string(plain)) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	got, err := box.Open(ct)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("plain mismatch: got %q want %q", got, plain)
	}
}

func TestNonceIsRandomized(t *testing.T) {
	box := newTestBox(t)
	a, _ := box.Seal([]byte("same input"))
	b, _ := box.Seal([]byte("same input"))
	if a == b {
		t.Fatalf("two seals of the same plaintext must differ (nonce must be random)")
	}
}

func TestOpenFailsOnWrongKey(t *testing.T) {
	box1 := newTestBox(t)
	box2 := newTestBox(t)
	ct, _ := box1.Seal([]byte("payload"))
	if _, err := box2.Open(ct); err == nil {
		t.Fatal("expected open with wrong key to fail")
	}
}

func TestNewFromHex_BadInput(t *testing.T) {
	if _, err := NewFromHex(""); err == nil {
		t.Fatal("empty key should error")
	}
	if _, err := NewFromHex("not-hex"); err == nil {
		t.Fatal("non-hex should error")
	}
	if _, err := NewFromHex(strings.Repeat("aa", 16)); err == nil {
		t.Fatal("short key (16 bytes) should error")
	}
}

func newTestBox(t *testing.T) *Box {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("rand: %v", err)
	}
	box, err := NewFromHex(hex.EncodeToString(raw))
	if err != nil {
		t.Fatalf("NewFromHex: %v", err)
	}
	return box
}
