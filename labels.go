package gobellim

import (
	"encoding/json"
	"fmt"
)

// Integrity says how far a value may be relied on as a basis for an action.
// The zero value is the least trusted one on purpose.
type Integrity uint8

const (
	// Untrusted marks text supplied by a user, a document or an external tool.
	Untrusted Integrity = iota
	// Derived marks model output: a schema limits its shape, not its truth.
	Derived
	// Validated marks a value promoted by an explicit validator.
	Validated
	// Trusted marks application or principal state.
	Trusted
)

// String returns the tag name of the integrity level.
func (i Integrity) String() string {
	switch i {
	case Untrusted:
		return "untrusted"
	case Derived:
		return "derived"
	case Validated:
		return "validated"
	case Trusted:
		return "trusted"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the integrity level as its tag name.
func (i Integrity) MarshalJSON() ([]byte, error) { return json.Marshal(i.String()) }

// UnmarshalJSON decodes an integrity level from its tag name.
func (i *Integrity) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, ok := parseIntegrity(s)
	if !ok {
		return fmt.Errorf("%w: unknown integrity %q", ErrInvalidConfig, s)
	}
	*i = v
	return nil
}

func parseIntegrity(s string) (Integrity, bool) {
	switch s {
	case "untrusted":
		return Untrusted, true
	case "derived":
		return Derived, true
	case "validated":
		return Validated, true
	case "trusted":
		return Trusted, true
	default:
		return Untrusted, false
	}
}

// Sensitivity sets disclosure limits. It is independent of Integrity.
type Sensitivity uint8

const (
	// Public may be disclosed freely.
	Public Sensitivity = iota
	// Internal is disclosed inside the tenant.
	Internal
	// PII is personal data and needs masking.
	PII
	// Secret must not reach the model or a log in clear text.
	Secret
)

// String returns the tag name of the sensitivity level.
func (s Sensitivity) String() string {
	switch s {
	case Public:
		return "public"
	case Internal:
		return "internal"
	case PII:
		return "pii"
	case Secret:
		return "secret"
	default:
		return "unknown"
	}
}

// MarshalJSON encodes the sensitivity level as its tag name.
func (s Sensitivity) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON decodes a sensitivity level from its tag name.
func (s *Sensitivity) UnmarshalJSON(b []byte) error {
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return err
	}
	v, ok := parseSensitivity(str)
	if !ok {
		return fmt.Errorf("%w: unknown sensitivity %q", ErrInvalidConfig, str)
	}
	*s = v
	return nil
}

func parseSensitivity(s string) (Sensitivity, bool) {
	switch s {
	case "public":
		return Public, true
	case "internal":
		return Internal, true
	case "pii":
		return PII, true
	case "secret":
		return Secret, true
	default:
		return Public, false
	}
}

// SourceRef names the origin of a value without carrying the value itself.
type SourceRef struct {
	Kind string `json:"kind"`
	ID   string `json:"id,omitempty"`
}

// Label carries provenance for one value.
type Label struct {
	Integrity   Integrity
	Sources     []SourceRef
	Sensitivity Sensitivity
}

func worseIntegrity(a, b Integrity) Integrity {
	if a < b {
		return a
	}
	return b
}

func maxSensitivity(a, b Sensitivity) Sensitivity {
	if a > b {
		return a
	}
	return b
}
