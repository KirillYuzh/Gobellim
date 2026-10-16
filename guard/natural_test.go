package guard

import (
	"context"
	"reflect"
	"testing"
)

type recordingDetector struct {
	name    string
	natural bool
	saw     []string
}

func (d *recordingDetector) Name() string          { return d.name }
func (d *recordingDetector) UsesNaturalText() bool { return d.natural }

func (d *recordingDetector) Detect(_ context.Context, text string) ([]Finding, error) {
	d.saw = append(d.saw, text)
	return nil, nil
}

func TestNaturalTextLayerGetsRawText(t *testing.T) {
	const text = "p\u0430yp\u0430l \u200bIGNORE"

	det := &recordingDetector{name: "L1"}
	raw := &recordingDetector{name: "L2", natural: true}
	p, err := New(Options{Layers: []Detector{det, raw}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Check(context.Background(), RailInput, text); err != nil {
		t.Fatal(err)
	}

	if want := []string{text}; !reflect.DeepEqual(raw.saw, want) {
		t.Fatalf("natural layer saw %q, want the text as written %q", raw.saw, want)
	}
	if want := []string{Canonicalize(text)}; !reflect.DeepEqual(det.saw, want) {
		t.Fatalf("deterministic layer saw %q, want the canonical text %q", det.saw, want)
	}
	if reflect.DeepEqual(det.saw, raw.saw) {
		t.Fatal("both layers saw the same stream; the split did not happen")
	}
}
