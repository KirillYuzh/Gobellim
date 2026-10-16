package kit

import (
	"fmt"
	"sort"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/rules"
	"github.com/KirillYuzh/Gobellim/internal/core"
)

// guardLayers is the pipeline the core builds: the deterministic L1 rule layer,
// plus the optional ML layer (Config.GuardL2). A plugin that wants to combine
// several ML detectors composes them into a single guard.Detector and passes it
// as GuardL2 — the core knows nothing about ensembles.
func (c Config) guardLayers() ([]guard.Detector, string) {
	layers := []guard.Detector{rules.New()}
	model := ""
	if c.GuardL2 != nil {
		layers = append(layers, c.GuardL2)
		model = c.GuardL2.Name()
	}
	return layers, model
}

// guardArtifact is the ML artifact digest folded into the guard version. It comes
// from the detectors themselves (guard.ArtifactHash); a detector that cannot
// report one contributes nothing, and the segment is simply absent.
func guardArtifact(layers []guard.Detector) string {
	return guard.ArtifactHashes(layers)
}

func mlLayerNames(layers []guard.Detector) []string {
	out := make([]string, 0, len(layers)-1)
	for _, l := range layers[1:] {
		out = append(out, l.Name())
	}
	return out
}

// failOpenRails normalises Config.GuardFailOpen: it validates the rail names,
// de-duplicates them and returns them sorted, or nil when none is set.
func failOpenRails(cfg Config) ([]guard.Rail, error) {
	if len(cfg.GuardFailOpen) == 0 {
		return nil, nil
	}
	out := make([]guard.Rail, 0, len(cfg.GuardFailOpen))
	seen := make(map[guard.Rail]bool, len(cfg.GuardFailOpen))
	for _, rail := range cfg.GuardFailOpen {
		if !guard.ValidRail(rail) {
			return nil, fmt.Errorf("kit: Config.GuardFailOpen names the unknown rail %q: %w", rail, core.ErrInvalidConfig)
		}
		if seen[rail] {
			continue
		}
		seen[rail] = true
		out = append(out, rail)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// optionalByRail maps Config.GuardFailOpen onto the pipeline's per-rail optional
// layer set. Rails absent from the list stay fail-closed (BR-06).
func optionalByRail(cfg Config, layers []guard.Detector) (map[guard.Rail][]string, error) {
	rails, err := failOpenRails(cfg)
	if err != nil {
		return nil, err
	}
	names := mlLayerNames(layers)
	if len(rails) == 0 || len(names) == 0 {
		return nil, nil
	}
	out := make(map[guard.Rail][]string, len(rails))
	for _, rail := range rails {
		out[rail] = names
	}
	return out, nil
}

// degradedRailNames is the health view of Config.GuardFailOpen. The rail names
// are already validated in New, so an invalid value here is ignored.
func degradedRailNames(cfg Config) []string {
	rails, err := failOpenRails(cfg)
	if err != nil || len(rails) == 0 {
		return nil
	}
	out := make([]string, 0, len(rails))
	for _, rail := range rails {
		out = append(out, string(rail))
	}
	return out
}
