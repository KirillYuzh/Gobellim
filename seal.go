package gobellim

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
)

// Sealer signs the messages that bind an approval to the process that issued it.
// Two structures carry a decision across a request boundary: PendingAction in
// this package and flow.Suspension in the optional workflow package. Both reach
// the application as plain JSON, so both need the same guarantee: an object this
// process did not issue must not be executable.
//
// A Sealer created from a random key is process-bound. Pass the same 32-byte key
// to every replica when an approval may be decided on another one.
type Sealer struct{ key []byte }

// NewSealer returns a Sealer over key. The key must be 32 bytes.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("%w: sealer key must be 32 bytes", ErrInvalidConfig)
	}
	return &Sealer{key: append([]byte(nil), key...)}, nil
}

// Sign returns the HMAC-SHA256 of msg.
func (s *Sealer) Sign(msg []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(msg)
	return m.Sum(nil)
}

// Verify reports whether proof is the signature of msg. The comparison is
// constant time. An empty proof is never a valid signature.
func (s *Sealer) Verify(msg, proof []byte) bool {
	return len(proof) > 0 && hmac.Equal(proof, s.Sign(msg))
}
