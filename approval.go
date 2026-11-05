package gobellim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// Provenance explains where one action argument came from.
type Provenance struct {
	Arg       string    `json:"arg"`
	Source    string    `json:"source"`
	Integrity Integrity `json:"integrity"`
}

// ApprovalCard is the typed payload a human decides on. The UI belongs to the
// service; the card only carries the facts needed for a meaningful decision.
// Args contains the resolved values the action would use, so the service must
// apply its approver ACL before displaying the card.
type ApprovalCard struct {
	RunID              string          `json:"run_id,omitempty"`
	Flow               string          `json:"flow,omitempty"`
	Step               string          `json:"step,omitempty"`
	Tool               string          `json:"tool"`
	Effect             Effect          `json:"effect"`
	Args               json.RawMessage `json:"args,omitempty"`
	Digest             string          `json:"digest"`
	Reason             string          `json:"reason,omitempty"`
	Provenance         []Provenance    `json:"provenance,omitempty"`
	RequiredPermission string          `json:"required_permission,omitempty"`
	FourEyes           bool            `json:"four_eyes,omitempty"`
	ExpiresAt          time.Time       `json:"expires_at,omitempty"`
}

// ActionDigest is the stable digest of the tool name and canonical arguments.
// Changing either invalidates an earlier approval.
func (c ApprovalCard) ActionDigest() string {
	return actionDigest(c.Tool, c.Args)
}

func actionDigest(tool string, args []byte) string {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	h.Write(args)
	return hex.EncodeToString(h.Sum(nil))
}

func (c ApprovalCard) normalize() ApprovalCard {
	if c.Digest == "" {
		c.Digest = c.ActionDigest()
	}
	return c
}
