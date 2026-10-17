package eval

import "fmt"

// RetrievalMetrics summarises one ranked retrieval result against a labelled set
// of relevant document IDs.
type RetrievalMetrics struct {
	// K is the cut-off actually used.
	K int
	// Retrieved is the number of distinct results considered, capped at K.
	Retrieved int
	// Relevant is the number of distinct labelled documents.
	Relevant int
	// Hits is the number of relevant documents inside the cut-off.
	Hits int
	// Precision is Hits / Retrieved.
	Precision float64
	// Recall is Hits / Relevant.
	Recall float64
	// MRR is the reciprocal rank of the first relevant result, or 0.
	MRR float64
}

// ScoreRetrieval computes precision at K, recall at K and MRR for a ranked list
// of document IDs. A non-positive K uses the whole list. Duplicate IDs count
// once, and precision divides by the number of results actually returned (capped
// at K), so a short list is not penalised for entries that do not exist. An
// empty relevant set yields zero metrics.
func ScoreRetrieval(ranked []string, relevant []string, k int) RetrievalMetrics {
	want := make(map[string]bool, len(relevant))
	for _, id := range relevant {
		want[id] = true
	}

	seen := make(map[string]bool, len(ranked))
	order := make([]string, 0, len(ranked))
	for _, id := range ranked {
		if !seen[id] {
			seen[id] = true
			order = append(order, id)
		}
	}
	if k <= 0 || k > len(order) {
		k = len(order)
	}

	m := RetrievalMetrics{K: k, Retrieved: k, Relevant: len(want)}
	for i, id := range order[:k] {
		if want[id] {
			m.Hits++
			if m.MRR == 0 {
				m.MRR = 1 / float64(i+1)
			}
		}
	}
	if m.Retrieved > 0 {
		m.Precision = float64(m.Hits) / float64(m.Retrieved)
	}
	if m.Relevant > 0 {
		m.Recall = float64(m.Hits) / float64(m.Relevant)
	}
	return m
}

// BinaryMetrics is a confusion matrix for a binary classifier such as a guard
// detector that flags or allows one case.
type BinaryMetrics struct {
	TP int
	FP int
	FN int
	TN int
	// Precision is TP / (TP + FP).
	Precision float64
	// Recall is TP / (TP + FN).
	Recall float64
	// F1 is the harmonic mean of precision and recall.
	F1 float64
	// FalsePositiveRate is FP / (FP + TN).
	FalsePositiveRate float64
}

// ScoreBinary compares expected and actual labels and returns the confusion
// matrix with precision, recall, F1 and the false-positive rate. It returns an
// error when the slices differ in length. An undefined ratio is 0.
func ScoreBinary(expected, actual []bool) (BinaryMetrics, error) {
	if len(expected) != len(actual) {
		return BinaryMetrics{}, fmt.Errorf("eval: expected %d labels, got %d", len(expected), len(actual))
	}
	var m BinaryMetrics
	for i := range expected {
		switch {
		case expected[i] && actual[i]:
			m.TP++
		case !expected[i] && actual[i]:
			m.FP++
		case expected[i] && !actual[i]:
			m.FN++
		default:
			m.TN++
		}
	}
	if d := m.TP + m.FP; d > 0 {
		m.Precision = float64(m.TP) / float64(d)
	}
	if d := m.TP + m.FN; d > 0 {
		m.Recall = float64(m.TP) / float64(d)
	}
	if m.Precision+m.Recall > 0 {
		m.F1 = 2 * m.Precision * m.Recall / (m.Precision + m.Recall)
	}
	if d := m.FP + m.TN; d > 0 {
		m.FalsePositiveRate = float64(m.FP) / float64(d)
	}
	return m, nil
}
