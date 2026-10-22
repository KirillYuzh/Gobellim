package guard

import (
	"context"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

type l1Only struct{}

// L1Only is the explicit opt-out of the ML layer. Pass it as
// Config.GuardL2 when the deployment has no ML detector (D80): the
// composition stays deterministic (L0/L1) on purpose, not by accident. A nil
// Config.GuardL2 is a configuration error, so the weaker setup is always
// requested, never inherited.
func L1Only() Detector { return l1Only{} }

// Name reports the deterministic layer, not a second one: Config.guardLayers
// drops the marker, so the pipeline's layer list stays exactly `L1`.
func (l1Only) Name() string { return rulesLayer }

func (l1Only) Detect(context.Context, string) ([]Finding, error) {
	return nil, core.ErrGuardUnavailable
}

// IsL1Only reports whether d is the explicit L1-only marker.
func IsL1Only(d Detector) bool {
	_, ok := d.(l1Only)
	return ok
}
