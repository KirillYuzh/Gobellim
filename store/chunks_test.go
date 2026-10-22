package store

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestValidateChunkRequiresIdentity(t *testing.T) {
	base := Chunk{ID: "c1", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Text: "hi", Vector: []float32{1, 0}}
	if err := ValidateChunk(base, 2); err != nil {
		t.Fatalf("valid chunk rejected: %v", err)
	}

	cases := map[string]func(*Chunk){
		"no id":     func(c *Chunk) { c.ID = "" },
		"no doc id": func(c *Chunk) { c.DocID = "" },
		"no tenant": func(c *Chunk) { c.Tenant = "" },
		"nil acl":   func(c *Chunk) { c.ACL = nil },
		"empty acl": func(c *Chunk) { c.ACL = []string{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			mutate(&c)
			if err := ValidateChunk(c, 2); !errors.Is(err, core.ErrInvalidArgs) {
				t.Fatalf("chunk with %s must fail with ErrInvalidArgs, got %v", name, err)
			}
		})
	}
}

func TestValidateChunkQuarantineRequiresIdentity(t *testing.T) {
	q := Chunk{ID: "q1", DocID: "d1", Tenant: "t1", ACL: []string{"support"}, Quarantine: true}
	if err := ValidateChunk(q, 2); err != nil {
		t.Fatalf("clean quarantine chunk rejected: %v", err)
	}

	cases := map[string]func(*Chunk){
		"no id":     func(c *Chunk) { c.ID = "" },
		"no doc id": func(c *Chunk) { c.DocID = "" },
		"no tenant": func(c *Chunk) { c.Tenant = "" },
		"nil acl":   func(c *Chunk) { c.ACL = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := q
			mutate(&c)
			if err := ValidateChunk(c, 2); !errors.Is(err, core.ErrInvalidArgs) {
				t.Fatalf("quarantined chunk with %s must fail with ErrInvalidArgs, got %v", name, err)
			}
		})
	}
}

func TestChunkDocumentWritesEmptyACL(t *testing.T) {
	doc := Chunk{ID: "c1", DocID: "d1", Tenant: "t1"}.Document()
	acl, ok := doc[ACLField].([]string)
	if !ok {
		t.Fatalf("acl must be a []string, got %T", doc[ACLField])
	}
	if len(acl) != 0 {
		t.Fatalf("acl = %v, want empty", acl)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"acl":null`) {
		t.Fatalf("acl must never marshal as null: %s", raw)
	}
}
