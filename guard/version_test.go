package guard

import (
	"context"
	"strings"
	"testing"
)

type artifactDetector struct {
	fakeDetector
	hash string
}

func (d *artifactDetector) ArtifactHash() string { return d.hash }

func TestArtifactHashesIsEmptyWithoutArtifacts(t *testing.T) {
	layers := []Detector{&fakeDetector{name: "L1"}, &fakeDetector{name: "L2"}}
	if got := ArtifactHashes(layers); got != "" {
		t.Fatalf("ArtifactHashes = %q, want empty: no layer reports an artifact", got)
	}
	if got := ArtifactHashes(nil); got != "" {
		t.Fatalf("ArtifactHashes(nil) = %q", got)
	}
}

func TestArtifactHashesIsStableAndOrderFree(t *testing.T) {
	a := &artifactDetector{fakeDetector: fakeDetector{name: "L2"}, hash: "aaaa"}
	b := &artifactDetector{fakeDetector: fakeDetector{name: "L2b"}, hash: "bbbb"}
	plain := &fakeDetector{name: "L1"}

	both := ArtifactHashes([]Detector{a, b, plain})
	if both == "" {
		t.Fatal("ArtifactHashes = empty, want a digest")
	}
	if again := ArtifactHashes([]Detector{plain, b, a}); again != both {
		t.Fatalf("ArtifactHashes depends on the layer order: %q vs %q", again, both)
	}
	if len(both) > 32 {
		t.Fatalf("ArtifactHashes = %q, want a short digest", both)
	}
	if dup := ArtifactHashes([]Detector{a, a}); dup == "" {
		t.Fatal("a repeated artifact must still produce a digest")
	}

	c := &artifactDetector{fakeDetector: fakeDetector{name: "L2c"}, hash: "cccc"}
	if other := ArtifactHashes([]Detector{c, plain}); other == both {
		t.Fatalf("a different artifact produced the same digest %q", other)
	}
	empty := &artifactDetector{fakeDetector: fakeDetector{name: "L2e"}, hash: ""}
	if got := ArtifactHashes([]Detector{empty}); got != "" {
		t.Fatalf("an empty hash must not produce a digest, got %q", got)
	}
}

func TestVersionKeepsCallerVersionWithoutArtifact(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}, Version: "v1"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Version() != "v1" {
		t.Fatalf("Version() = %q, want the caller version unchanged", p.Version())
	}
}

func TestVersionFoldsTheArtifactHash(t *testing.T) {
	hash := ArtifactHashes([]Detector{&artifactDetector{hash: "aaaa"}})
	if hash == "" {
		t.Fatal("fixture must report an artifact")
	}

	explicit, err := New(Options{
		Layers:       []Detector{&fakeDetector{name: "L1"}},
		Version:      "policy/L1",
		ArtifactHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := "policy/L1/" + hash; explicit.Version() != want {
		t.Fatalf("Version() = %q, want %q", explicit.Version(), want)
	}

	already, err := New(Options{
		Layers:       []Detector{&fakeDetector{name: "L1"}},
		Version:      "policy/L1/" + hash,
		ArtifactHash: hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if already.Version() != "policy/L1/"+hash {
		t.Fatalf("Version() = %q, want no duplicated segment", already.Version())
	}

	derived, err := New(Options{Layers: []Detector{
		&artifactDetector{fakeDetector: fakeDetector{name: "L2"}, hash: "aaaa"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(derived.Version(), "/"+hash) {
		t.Fatalf("derived Version() = %q, want it to end with the artifact hash %q", derived.Version(), hash)
	}
}

func TestArtifactChangeInvalidatesTheVerdictCache(t *testing.T) {
	const text = "my card is 4111 1111 1111 1111"
	ctx := context.Background()

	d := &artifactDetector{fakeDetector: fakeDetector{name: "L2"}, hash: "artifact-a"}
	first, err := New(Options{Layers: []Detector{d}, Version: "policy/L1+L2", CacheSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Check(ctx, RailInput, text); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Check(ctx, RailInput, text); err != nil {
		t.Fatal(err)
	}
	if d.calls != 1 {
		t.Fatalf("detector ran %d times, want 1 (the second scan must be a cache hit)", d.calls)
	}
	versionA := first.Version()

	d.hash = "artifact-b"
	second, err := New(Options{Layers: []Detector{d}, Version: "policy/L1+L2", CacheSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	if second.Version() == versionA {
		t.Fatalf("the artifact must change the version: both are %q", versionA)
	}
	if _, err := second.Check(ctx, RailInput, text); err != nil {
		t.Fatal(err)
	}
	if d.calls != 2 {
		t.Fatalf("detector ran %d times, want 2: the new artifact must not reuse the cached verdict", d.calls)
	}
	if _, err := second.Check(ctx, RailInput, text); err != nil {
		t.Fatal(err)
	}
	if d.calls != 2 {
		t.Fatalf("detector ran %d times, want 2 (the second scan of the new artifact must hit)", d.calls)
	}
}
