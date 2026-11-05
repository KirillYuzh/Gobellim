package flow

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// Sealer seals and opens continuation tokens with AES-GCM. The token carries
// its key ID so a rotation can keep opening old tokens while sealing new ones
// with the primary key.
type Sealer struct {
	primary string
	keys    map[string]cipher.AEAD
}

// NewSealer builds a sealer with one primary key. The key must be 16, 24 or
// 32 bytes.
func NewSealer(keyID string, key []byte) (*Sealer, error) {
	s := &Sealer{primary: keyID, keys: make(map[string]cipher.AEAD)}
	if err := s.addKey(keyID, key); err != nil {
		return nil, err
	}
	return s, nil
}

// WithKey adds a secondary key, for example the previous key during a
// rotation. It returns the same sealer.
func (s *Sealer) WithKey(keyID string, key []byte) (*Sealer, error) {
	if err := s.addKey(keyID, key); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Sealer) addKey(keyID string, key []byte) error {
	if keyID == "" {
		return fmt.Errorf("flow: key id is required")
	}
	switch len(key) {
	case 16, 24, 32:
	default:
		return fmt.Errorf("flow: key must be 16, 24 or 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("flow: cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("flow: gcm: %w", err)
	}
	s.keys[keyID] = aead
	return nil
}

// Seal encrypts plain with the primary key. The key ID and nonce are prefixed
// to the ciphertext; the AAD additionally binds the key ID.
func (s *Sealer) Seal(plain, aad []byte) (string, error) {
	aead, ok := s.keys[s.primary]
	if !ok {
		return "", fmt.Errorf("flow: primary key is missing")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("flow: nonce: %w", err)
	}
	ct := aead.Seal(nil, nonce, plain, s.aadFor(s.primary, aad))
	out := make([]byte, 0, len(s.primary)+1+len(nonce)+len(ct))
	out = append(out, s.primary...)
	out = append(out, 0)
	out = append(out, nonce...)
	out = append(out, ct...)
	return base64.RawURLEncoding.EncodeToString(out), nil
}

// Open decrypts a token. It fails closed on a malformed token, an unknown key
// ID, a wrong key or AAD, or a modified ciphertext.
func (s *Sealer) Open(token string, aad []byte) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, fmt.Errorf("flow: decode token: %w", err)
	}
	idx := bytes.IndexByte(raw, 0)
	if idx <= 0 {
		return nil, fmt.Errorf("flow: token has no key id")
	}
	keyID := string(raw[:idx])
	aead, ok := s.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("flow: unknown key id %q", keyID)
	}
	rest := raw[idx+1:]
	if len(rest) < aead.NonceSize() {
		return nil, fmt.Errorf("flow: token is too short")
	}
	nonce, ct := rest[:aead.NonceSize()], rest[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, s.aadFor(keyID, aad))
	if err != nil {
		return nil, fmt.Errorf("flow: open token: %w", err)
	}
	return plain, nil
}

func (s *Sealer) aadFor(keyID string, aad []byte) []byte {
	out := make([]byte, 0, len(keyID)+1+len(aad))
	out = append(out, keyID...)
	out = append(out, 0)
	out = append(out, aad...)
	return out
}
