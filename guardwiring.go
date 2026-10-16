package kit

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/guard/rules"
	"github.com/gobellim/kit/internal/core"
)

func (c Config) guardLayers() ([]guard.Detector, []guard.Ensemble, string) {
	layers := []guard.Detector{rules.New()}
	model := ""
	if c.GuardL2 != nil {
		layers = append(layers, c.GuardL2)
		model = c.GuardL2.Name()
	}
	ensembles := make([]guard.Ensemble, len(c.GuardEnsembles))
	copy(ensembles, c.GuardEnsembles)
	if model == "" && len(ensembles) > 0 {
		model = ensembleModel(ensembles)
	}
	return layers, ensembles, model
}

func ensembleModel(ensembles []guard.Ensemble) string {
	parts := make([]string, 0, len(ensembles))
	for _, e := range ensembles {
		parts = append(parts, e.Name+"("+strings.Join(detectorNames(e.Detectors), "+")+")")
	}
	return strings.Join(parts, "+")
}

func detectorNames(detectors []guard.Detector) []string {
	out := make([]string, 0, len(detectors))
	for _, d := range detectors {
		if d == nil {
			continue
		}
		out = append(out, d.Name())
	}
	return out
}

func guardArtifact(cfg Config, layers []guard.Detector, ensembles []guard.Ensemble) (string, error) {
	if h := cfg.GuardL2ArtifactSHA256; h != "" && !validSHA256Hex(h) {
		return "", fmt.Errorf("kit: Config.GuardL2ArtifactSHA256 must be 64 lowercase hex characters, got %d characters: %w",
			len(h), core.ErrInvalidConfig)
	}
	detectors := make([]guard.Detector, 0, len(layers)+len(ensembles))
	detectors = append(detectors, layers...)
	for _, e := range ensembles {
		detectors = append(detectors, e.Detectors...)
	}
	if h := guard.ArtifactHashes(detectors); h != "" {
		return h, nil
	}
	return cfg.GuardL2ArtifactSHA256, nil
}

func validSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func mlLayerNames(layers []guard.Detector, ensembles []guard.Ensemble) []string {
	var out []string
	for _, l := range layers[1:] {
		out = append(out, l.Name())
	}
	for _, e := range ensembles {
		out = append(out, e.Name)
		out = append(out, detectorNames(e.Detectors)...)
	}
	return out
}

func optionalEverywhere(cfg Config, layers []guard.Detector, ensembles []guard.Ensemble) []string {
	if cfg.GuardMode != GuardDegraded {
		return nil
	}
	return mlLayerNames(layers, ensembles)
}

func degradedRailNames(cfg Config) []string {
	if len(cfg.GuardDegradedRails) == 0 {
		return nil
	}
	out := make([]string, 0, len(cfg.GuardDegradedRails))
	for rail, on := range cfg.GuardDegradedRails {
		if on {
			out = append(out, rail)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func optionalByRail(cfg Config, layers []guard.Detector, ensembles []guard.Ensemble) (map[guard.Rail][]string, error) {
	if len(cfg.GuardDegradedRails) == 0 {
		return nil, nil
	}
	rails := make([]guard.Rail, 0, len(cfg.GuardDegradedRails))
	for name := range cfg.GuardDegradedRails {
		rail := guard.Rail(name)
		if !guard.ValidRail(rail) {
			return nil, fmt.Errorf("kit: Config.GuardDegradedRails names the unknown rail %q: %w", name, core.ErrInvalidConfig)
		}
		rails = append(rails, rail)
	}
	names := mlLayerNames(layers, ensembles)
	if len(names) == 0 {
		return nil, nil
	}
	out := make(map[guard.Rail][]string, len(rails))
	for _, rail := range rails {
		if cfg.GuardDegradedRails[string(rail)] {
			out[rail] = names
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
