package guard

import (
	"fmt"

	"github.com/gobellim/kit/internal/core"
)

type Ensemble struct {
	Name      string
	Detectors []Detector
}

type layer struct {
	name      string
	detectors []Detector
}

const rulesLayer = "L1"

func buildLayers(flat []Detector, ensembles []Ensemble) ([]layer, error) {
	out := make([]layer, 0, len(flat)+len(ensembles))
	seen := make(map[string]bool, len(flat)+len(ensembles))
	for _, d := range flat {
		if d == nil {
			return nil, fmt.Errorf("guard: nil layer: %w", core.ErrInvalidConfig)
		}
		if seen[d.Name()] {
			return nil, fmt.Errorf("guard: duplicate layer %q: %w", d.Name(), core.ErrInvalidConfig)
		}
		seen[d.Name()] = true
		out = append(out, layer{name: d.Name(), detectors: []Detector{d}})
	}
	for _, e := range ensembles {
		if e.Name == "" {
			return nil, fmt.Errorf("guard: ensemble without a name: %w", core.ErrInvalidConfig)
		}
		if seen[e.Name] {
			return nil, fmt.Errorf("guard: duplicate layer %q: %w", e.Name, core.ErrInvalidConfig)
		}
		if len(e.Detectors) == 0 {
			return nil, fmt.Errorf("guard: ensemble %q has no detectors: %w", e.Name, core.ErrInvalidConfig)
		}
		for _, d := range e.Detectors {
			if d == nil {
				return nil, fmt.Errorf("guard: ensemble %q holds a nil detector: %w", e.Name, core.ErrInvalidConfig)
			}
		}
		seen[e.Name] = true
		out = append(out, layer{name: e.Name, detectors: append([]Detector(nil), e.Detectors...)})
	}
	return out, nil
}

func dedupe(findings []Finding) []Finding {
	if len(findings) < 2 {
		return findings
	}
	kept := findings[:0]
	for _, f := range findings {
		if coveredByKept(kept, f) {
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

func coveredByKept(kept []Finding, f Finding) bool {
	for _, k := range kept {
		if k.Category != f.Category || k.Label != f.Label {
			continue
		}
		if k.Start <= f.Start && f.End <= k.End {
			return true
		}
	}
	return false
}
