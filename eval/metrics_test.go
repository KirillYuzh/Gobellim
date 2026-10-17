package eval

import (
	"math"
	"testing"
)

func TestScoreRetrieval(t *testing.T) {
	cases := []struct {
		name     string
		ranked   []string
		relevant []string
		k        int
		want     RetrievalMetrics
	}{
		{
			name:     "cut-off drops a relevant hit",
			ranked:   []string{"a", "b", "c"},
			relevant: []string{"a", "c"},
			k:        2,
			want:     RetrievalMetrics{K: 2, Retrieved: 2, Relevant: 2, Hits: 1, Precision: 0.5, Recall: 0.5, MRR: 1},
		},
		{
			name:     "reciprocal rank of a later hit",
			ranked:   []string{"x", "a", "b"},
			relevant: []string{"a"},
			k:        3,
			want:     RetrievalMetrics{K: 3, Retrieved: 3, Relevant: 1, Hits: 1, Precision: 1.0 / 3, Recall: 1, MRR: 0.5},
		},
		{
			name:     "duplicates count once",
			ranked:   []string{"a", "a", "b"},
			relevant: []string{"a", "b"},
			k:        2,
			want:     RetrievalMetrics{K: 2, Retrieved: 2, Relevant: 2, Hits: 2, Precision: 1, Recall: 1, MRR: 1},
		},
		{
			name:     "non-positive k uses the whole list",
			ranked:   []string{"a", "b"},
			relevant: []string{"b"},
			k:        0,
			want:     RetrievalMetrics{K: 2, Retrieved: 2, Relevant: 1, Hits: 1, Precision: 0.5, Recall: 1, MRR: 0.5},
		},
		{
			name:     "short list is not penalised",
			ranked:   []string{"a"},
			relevant: []string{"a", "b"},
			k:        5,
			want:     RetrievalMetrics{K: 1, Retrieved: 1, Relevant: 2, Hits: 1, Precision: 1, Recall: 0.5, MRR: 1},
		},
		{
			name:     "empty relevant set",
			ranked:   []string{"a"},
			relevant: nil,
			k:        1,
			want:     RetrievalMetrics{K: 1, Retrieved: 1},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ScoreRetrieval(c.ranked, c.relevant, c.k)
			if got != c.want {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestScoreBinary(t *testing.T) {
	got, err := ScoreBinary(
		[]bool{true, false, true, false},
		[]bool{true, true, false, false},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := BinaryMetrics{TP: 1, FP: 1, FN: 1, TN: 1, Precision: 0.5, Recall: 0.5, F1: 0.5, FalsePositiveRate: 0.5}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	t.Run("no positives", func(t *testing.T) {
		got, err := ScoreBinary([]bool{false, false}, []bool{false, false})
		if err != nil {
			t.Fatal(err)
		}
		if got.TN != 2 || got.Precision != 0 || got.Recall != 0 || got.F1 != 0 || got.FalsePositiveRate != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestScoreBinaryLengthMismatch(t *testing.T) {
	if _, err := ScoreBinary([]bool{true}, nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestScoreRetrievalMRRBounds(t *testing.T) {
	got := ScoreRetrieval([]string{"a", "b", "c"}, []string{"c"}, 3)
	if math.Abs(got.MRR-1.0/3) > 1e-9 {
		t.Fatalf("MRR = %v", got.MRR)
	}
}
