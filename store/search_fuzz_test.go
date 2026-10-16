package store

import (
	"reflect"
	"strings"
	"testing"
)

func hitsFromIDs(ids []string) []Hit {
	out := make([]Hit, len(ids))
	for i, id := range ids {
		out[i] = Hit{ID: id}
	}
	return out
}

func FuzzRRF(f *testing.F) {
	f.Add("a\nb\nc", "b\na", 60, 10)
	f.Add("a\na\nb", "", 0, 0)
	f.Add("", "", -1, 3)
	f.Add("x", "x", 1, 1)
	f.Fuzz(func(t *testing.T, first, second string, rankConstant, topK int) {
		lists := [][]Hit{
			hitsFromIDs(strings.Split(first, "\n")),
			hitsFromIDs(strings.Split(second, "\n")),
		}
		out := RRF(lists, rankConstant, topK)

		if topK > 0 && len(out) > topK {
			t.Fatalf("RRF returned %d rows, over topK=%d", len(out), topK)
		}

		distinct := make(map[string]bool)
		for _, h := range lists[0] {
			distinct[h.ID] = true
		}
		for _, h := range lists[1] {
			distinct[h.ID] = true
		}
		if len(out) > len(distinct) {
			t.Fatalf("RRF returned %d rows for %d distinct ids", len(out), len(distinct))
		}

		seen := make(map[string]bool, len(out))
		for i, h := range out {
			if seen[h.ID] {
				t.Fatalf("RRF returned id %q twice", h.ID)
			}
			seen[h.ID] = true
			if i == 0 {
				continue
			}
			prev := out[i-1]
			if h.Score > prev.Score {
				t.Fatalf("RRF is not ordered by score: %v after %v", h, prev)
			}
			if h.Score == prev.Score && h.ID < prev.ID {
				t.Fatalf("RRF tie-break is not by id: %v after %v", h, prev)
			}
		}
		if !reflect.DeepEqual(out, RRF(lists, rankConstant, topK)) {
			t.Fatal("RRF is not deterministic")
		}
	})
}
