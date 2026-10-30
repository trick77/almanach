// Package secret seals a source's token for the database: AES-256-GCM under
// one key from the environment, a fresh nonce per seal. Stdlib only. The
// clear token exists in memory for the length of one git command and is
// never logged, returned or written to disk.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the AES-256 key length.
const KeySize = 32

// ErrNoKey is Seal or Open without a configured key.
var ErrNoKey = errors.New("no secret key configured (ALMANACH_SECRET_KEY)")

// Key is the configured key; a zero Key seals nothing.
type Key struct {
	aead cipher.AEAD
}

// Parse reads a key as standard base64 of exactly 32 bytes; "" is a zero
// Key, which Enabled reports false.
func Parse(encoded string) (Key, error) {
	if encoded == "" {
		return Key{}, nil
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return Key{}, fmt.Errorf("not base64: %w", err)
	}
	if len(raw) != KeySize {
		return Key{}, fmt.Errorf("%d bytes decoded, want %d", len(raw), KeySize)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return Key{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Key{}, err
	}
	return Key{aead: aead}, nil
}

// Enabled reports whether a key is configured.
func (k Key) Enabled() bool { return k.aead != nil }

// Seal encrypts clear; the nonce leads the ciphertext.
func (k Key) Seal(clear string) ([]byte, error) {
	if k.aead == nil {
		return nil, ErrNoKey
	}
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return k.aead.Seal(nonce, nonce, []byte(clear), nil), nil
}

// Open decrypts what Seal produced.
func (k Key) Open(sealed []byte) (string, error) {
	if k.aead == nil {
		return "", ErrNoKey
	}
	n := k.aead.NonceSize()
	if len(sealed) < n {
		return "", errors.New("sealed token too short")
	}
	clear, err := k.aead.Open(nil, sealed[:n], sealed[n:], nil)
	if err != nil {
		return "", fmt.Errorf("open token: %w", err)
	}
	return string(clear), nil
}
