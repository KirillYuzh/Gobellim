package app

import (
	"context"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/rules"
)

// fastPathAllocBudget is the allocation ceiling for one check on the
// deterministic fast path (L1 only, cached verdict). Measured 70 allocs/op on
// darwin/arm64 with Go 1.24 — see docs/guides/performance.md. The ceiling has
// headroom on purpose: it catches a gross regression, not noise.
const fastPathAllocBudget = 100

func TestGuardFastPathAllocationBudget(t *testing.T) {
	p, err := guard.New(guard.Options{Layers: []guard.Detector{rules.New()}, Version: "budget"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	const text = "Привет, подскажи, как оформить возврат товара в течение 14 дней."
	if _, err := p.Check(ctx, guard.RailInput, text); err != nil {
		t.Fatal(err)
	}

	allocs := testing.AllocsPerRun(200, func() {
		if _, err := p.Check(ctx, guard.RailInput, text); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("fast path: %.0f allocs/op (budget %d)", allocs, fastPathAllocBudget)
	if allocs > fastPathAllocBudget {
		t.Fatalf("the fast path allocates %.0f per check, over the %d budget: see docs/guides/performance.md",
			allocs, fastPathAllocBudget)
	}
}
