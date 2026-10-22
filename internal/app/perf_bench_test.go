package app

import (
	"context"
	"strconv"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/guard/rules"
)

func benchPipeline(b *testing.B) *guard.Pipeline {
	b.Helper()
	p, err := guard.New(guard.Options{Layers: []guard.Detector{rules.New()}, Version: "bench"})
	if err != nil {
		b.Fatal(err)
	}
	return p
}

// BenchmarkGuardFastPathCacheHit is the steady state of the deterministic layer:
// the same clean input, answered from the verdict cache. This is the "fast path"
// the allocation gate tracks.
func BenchmarkGuardFastPathCacheHit(b *testing.B) {
	p := benchPipeline(b)
	ctx := context.Background()
	const text = "Привет, подскажи, как оформить возврат товара в течение 14 дней."
	if _, err := p.Check(ctx, guard.RailInput, text); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := p.Check(ctx, guard.RailInput, text); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGuardL1DistinctText is a full L1 scan on a fresh input (no cache hit) —
// the realistic per-request cost of the deterministic layer.
func BenchmarkGuardL1DistinctText(b *testing.B) {
	p := benchPipeline(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		text := "Заказ " + strconv.Itoa(i) + ": подскажи, как оформить возврат товара в течение 14 дней."
		if _, err := p.Check(ctx, guard.RailInput, text); err != nil {
			b.Fatal(err)
		}
	}
}
