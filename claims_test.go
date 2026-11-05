package gobellim

import (
	"context"
	"testing"
	"time"
)

func TestMemClaims(t *testing.T) {
	m := NewMemClaims()
	ctx := context.Background()

	if ok, err := m.Claim(ctx, "n1", time.Now().Add(time.Hour)); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if ok, err := m.Claim(ctx, "n1", time.Now().Add(time.Hour)); err != nil || ok {
		t.Fatalf("second claim: ok=%v err=%v", ok, err)
	}
	if ok, _ := m.Claim(ctx, "n2", time.Now().Add(-time.Second)); !ok {
		t.Fatal("expired nonce was refused on first use")
	}
	if ok, _ := m.Claim(ctx, "n2", time.Now().Add(time.Hour)); !ok {
		t.Fatal("expired nonce was not reusable")
	}
	m.Forget(time.Now().Add(2 * time.Hour))
	if ok, _ := m.Claim(ctx, "n2", time.Now().Add(time.Hour)); !ok {
		t.Fatal("Forget did not drop the expired nonce")
	}
}
