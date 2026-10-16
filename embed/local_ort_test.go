//go:build ort

package embed

import (
	"math"
	"testing"
)

func TestMeanPoolAveragesMaskedTokens(t *testing.T) {
	hidden := []float32{
		0, 2,
		2, 4,
		9, 9,
	}
	mask := []int64{1, 1, 0}
	got := meanPool(hidden, mask, 3, 2)
	want := []float32{1, 3}
	for i := range want {
		if math.Abs(float64(got[i]-want[i])) > 1e-6 {
			t.Fatalf("meanPool = %v, want %v", got, want)
		}
	}
}

func TestMeanPoolAllMaskedIsZero(t *testing.T) {
	got := meanPool([]float32{5, 6, 7, 8}, []int64{0, 0}, 2, 2)
	for i, v := range got {
		if v != 0 {
			t.Fatalf("meanPool[%d] = %v, want 0", i, v)
		}
	}
}

func TestCLSPoolReturnsFirstToken(t *testing.T) {
	hidden := []float32{
		1, 2,
		3, 4,
		5, 6,
	}
	got := clsPool(hidden, 3, 2)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("clsPool = %v, want [1 2]", got)
	}
}

func TestCLSPoolEmptyIsZero(t *testing.T) {
	got := clsPool(nil, 0, 2)
	if len(got) != 2 || got[0] != 0 || got[1] != 0 {
		t.Fatalf("clsPool = %v, want [0 0]", got)
	}
}

func TestOrderByLengthIsStable(t *testing.T) {
	lengths := []int{5, 1, 3, 1}
	perm := orderByLength(lengths)
	want := []int{1, 3, 2, 0}
	for i := range want {
		if perm[i] != want[i] {
			t.Fatalf("orderByLength = %v, want %v", perm, want)
		}
	}
}

func TestRestoreOrderInvertsPermutation(t *testing.T) {
	lengths := []int{5, 1, 3, 1}
	perm := orderByLength(lengths)

	vectors := make([][]float32, len(perm))
	for k, src := range perm {
		vectors[k] = []float32{float32(src)}
	}
	restored := restoreOrder(vectors, perm)
	for i := range restored {
		if restored[i][0] != float32(i) {
			t.Fatalf("restoreOrder[%d] = %v, want %d", i, restored[i][0], i)
		}
	}
}
