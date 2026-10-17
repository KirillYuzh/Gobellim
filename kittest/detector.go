package kittest

import (
	"context"
	"strings"

	"github.com/KirillYuzh/Gobellim/guard"
)

// DetectFunc is the function Detector uses to produce findings for a text.
type DetectFunc func(ctx context.Context, text string) ([]guard.Finding, error)

// Detector is a deterministic guard.Detector driven by Find instead of a model.
type Detector struct {
	Label string
	Find  DetectFunc
}

// Name returns the detector's label.
func (d Detector) Name() string { return d.Label }

// Detect calls Find, or returns no findings when Find is nil.
func (d Detector) Detect(ctx context.Context, text string) ([]guard.Finding, error) {
	if d.Find == nil {
		return nil, nil
	}
	return d.Find(ctx, text)
}

// Findings reports the same findings for every input.
func Findings(label string, found ...guard.Finding) Detector {
	return Detector{Label: label, Find: func(context.Context, string) ([]guard.Finding, error) {
		return found, nil
	}}
}

// Match reports the findings only for inputs containing trigger.
func Match(label, trigger string, found ...guard.Finding) Detector {
	return Detector{Label: label, Find: func(_ context.Context, text string) ([]guard.Finding, error) {
		if strings.Contains(text, trigger) {
			return found, nil
		}
		return nil, nil
	}}
}

// Unavailable always fails — an unavailable ML layer (I-16).
func Unavailable(label string, err error) Detector {
	return Detector{Label: label, Find: func(context.Context, string) ([]guard.Finding, error) {
		return nil, err
	}}
}
