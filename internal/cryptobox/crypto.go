// Package cryptobox is a small AES-256-GCM seal/open helper used to encrypt
// at-rest secrets that the platform must be able to read back in plaintext
// (currently only webhook HMAC secrets). The key is a 32-byte value
// supplied via the API_SECRETS_KEY env var, hex-encoded.
//
// This is the v1 pattern. The v1.5 upgrade path is KMS-wrapped data keys.
package cryptobox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

// Box can Seal and Open opaque payloads with a single AES key.
type Box struct {
	aead cipher.AEAD
}

// NewFromHex parses a hex-encoded 32-byte key and returns a ready-to-use Box.
func NewFromHex(keyHex string) (*Box, error) {
	if keyHex == "" {
		return nil, errors.New("cryptobox: key is empty")
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: hex decode key: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("cryptobox: key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext and returns base64(nonce || ciphertext || tag).
func (b *Box) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := b.aead.Seal(nil, nonce, plaintext, nil)
	out := append(nonce, ct...)
	return base64.StdEncoding.EncodeToString(out), nil
}

// Open is the inverse of Seal. Returns an error on bad ciphertext or wrong key.
func (b *Box) Open(payload string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: base64: %w", err)
	}
	ns := b.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("cryptobox: payload too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	pt, err := b.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("cryptobox: open: %w", err)
	}
	return pt, nil
}
