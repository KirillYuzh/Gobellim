package gobellim

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"
)

// Provenance explains where one action argument came from.
type Provenance struct {
	Arg       string    `json:"arg"`
	Source    string    `json:"source"`
	Integrity Integrity `json:"integrity"`
}

// ApprovalCard is the typed payload a trusted approver decides on. The UI belongs to the
// service; the card only carries the facts needed for a meaningful decision.
// Args contains the resolved values the action would use, so the service must
// apply its approver ACL before displaying the card.
type ApprovalCard struct {
	RunID              string          `json:"run_id,omitempty"`
	Flow               string          `json:"flow,omitempty"`
	Step               string          `json:"step,omitempty"`
	Tool               string          `json:"tool"`
	Operation          Operation       `json:"operation"`
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

// proofMessage is the canonical byte string an issued PendingAction is signed
// over. Every field of the card is included, because the card is the evidence a
// approver decided on: a party that can rewrite the reason, provenance or required
// permission must not keep a valid action digest. Strings are length-prefixed so
// no two different field values produce the same message.
func proofMessage(p *PendingAction) []byte {
	b := make([]byte, 0, 512)
	add := func(s string) {
		b = strconv.AppendInt(b, int64(len(s)), 10)
		b = append(b, ':')
		b = append(b, s...)
		b = append(b, 0)
	}
	for _, s := range []string{p.RunID, p.Task, p.Tenant, p.Tool, p.Nonce, p.Digest} {
		add(s)
	}
	for _, s := range []string{
		p.Card.RunID, p.Card.Flow, p.Card.Step, p.Card.Tool,
		p.Card.Operation.String(), string(p.Card.Args), p.Card.Digest,
		p.Card.Reason, p.Card.RequiredPermission,
	} {
		add(s)
	}
	b = strconv.AppendBool(b, p.Card.FourEyes)
	b = append(b, 0)
	b = strconv.AppendInt(b, p.Card.ExpiresAt.UnixNano(), 10)
	b = append(b, 0)
	for _, pr := range p.Card.Provenance {
		add(pr.Arg)
		add(pr.Source)
		add(pr.Integrity.String())
	}
	return strconv.AppendInt(b, p.ExpiresAt.UnixNano(), 10)
}

// sign binds a pending action to this engine. Without it the struct is a bearer
// token: an approval endpoint that decodes one from a request body would let a
// client choose its own arguments, digest and nonce.
func (e *Engine) sign(p *PendingAction) []byte {
	return e.sealer.Sign(proofMessage(p))
}

func (e *Engine) signedByUs(p *PendingAction) bool {
	return e.sealer.Verify(proofMessage(p), p.Proof)
}
