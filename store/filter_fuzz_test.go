package store

import (
	"encoding/json"
	"testing"
)

// CompileFilter either rejects the filter or yields a clause that serialises for
// Elasticsearch; a non-empty filter must never quietly compile to nothing.
func FuzzCompileFilter(f *testing.F) {
	for _, seed := range []string{
		`{"doc_id":"a"}`,
		`{"position":{"$gt":10,"$lte":20}}`,
		`{"source":{"$in":["a","b"]}}`,
		`{"$or":[{"doc_id":"a"},{"source":"b"}]}`,
		`{"$not":{"quarantined":true}}`,
		`{"created_at":{"$exists":true}}`,
		`{"$and":[{}]}`,
		`{"nosuchfield":"x"}`,
		`{"doc_id":{"$bogus":1}}`,
		`[]`,
		`"scalar"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		var filter Filter
		if err := json.Unmarshal([]byte(raw), &filter); err != nil {
			return
		}
		clause, err := CompileFilter(filter)
		if err != nil {
			return
		}
		if clause == nil {
			if len(filter) > 0 {
				t.Fatalf("CompileFilter(%s) returned a nil clause without an error", raw)
			}
			return
		}
		if _, err := json.Marshal(clause); err != nil {
			t.Fatalf("CompileFilter(%s) produced an unmarshalable clause: %v", raw, err)
		}
	})
}
