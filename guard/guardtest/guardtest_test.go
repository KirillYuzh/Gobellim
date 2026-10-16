package guardtest

import (
	"context"
	"testing"

	"github.com/gobellim/kit/guard"
)

func TestAllowAll(t *testing.T) {
	d := AllowAll()
	if d.Name() != "allow-all" {
		t.Fatalf("Name() = %q, want allow-all", d.Name())
	}
	p, err := guard.New(guard.Options{Layers: []guard.Detector{d}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Check(context.Background(), guard.RailInput, "anything at all")
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != guard.Allow || v.Text != "anything at all" {
		t.Fatalf("AllowAll: %+v", v)
	}
}
