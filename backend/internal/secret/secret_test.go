package secret

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testKey(t *testing.T) Key {
	t.Helper()
	raw := make([]byte, KeySize)
	for i := range raw {
		raw[i] = byte(i)
	}
	k, err := Parse(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpen_RoundTrip(t *testing.T) {
	k := testKey(t)
	sealed, err := k.Seal("tok-123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sealed), "tok-123") {
		t.Fatal("token visible in the sealed bytes")
	}
	clear, err := k.Open(sealed)
	if err != nil || clear != "tok-123" {
		t.Fatalf("Open = %q, %v", clear, err)
	}
	// A fresh nonce every time: two seals of one token differ.
	again, _ := k.Seal("tok-123")
	if string(again) == string(sealed) {
		t.Fatal("two seals identical: nonce reused")
	}
}

func TestOpen_RejectsTampering(t *testing.T) {
	k := testKey(t)
	sealed, _ := k.Seal("tok")
	sealed[len(sealed)-1] ^= 1
	if _, err := k.Open(sealed); err == nil {
		t.Fatal("tampered ciphertext opened")
	}
	if _, err := k.Open([]byte("x")); err == nil {
		t.Fatal("short ciphertext opened")
	}
}

func TestParse(t *testing.T) {
	if k, err := Parse(""); err != nil || k.Enabled() {
		t.Fatalf("empty: %v, enabled=%v", err, k.Enabled())
	}
	if _, err := Parse("not base64!"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := Parse(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil || !strings.Contains(err.Error(), "want 32") {
		t.Fatalf("short key: %v", err)
	}
	if _, err := (Key{}).Seal("x"); err != ErrNoKey {
		t.Fatalf("zero key Seal: %v", err)
	}
}
