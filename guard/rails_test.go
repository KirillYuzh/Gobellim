package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

func TestRailsAreExactlyTheThreeWorkingOnes(t *testing.T) {
	want := []Rail{RailInput, RailOutput, RailToolResult}
	got := Rails()
	if len(got) != len(want) {
		t.Fatalf("Rails() = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Rails() = %v, want exactly %v", got, want)
		}
	}
	if RailInput != "input" || RailOutput != "output" || RailToolResult != "tool_result" {
		t.Fatalf("rail values changed: %q/%q/%q", RailInput, RailOutput, RailToolResult)
	}
}

var railDecl = regexp.MustCompile(`Rail\s*=\s*"([^"]*)"`)

func TestGuardFamilyDeclaresNoOtherRail(t *testing.T) {
	files, err := filepath.Glob("**/*.go")
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, "guard.go")
	declared := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range railDecl.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = f
		}
	}
	for _, r := range Rails() {
		delete(declared, string(r))
	}
	if len(declared) != 0 {
		t.Fatalf("guard declares rails outside the working set %v: %v", Rails(), declared)
	}
}

func TestValidRailRejectsUnknownRails(t *testing.T) {
	for _, r := range Rails() {
		if !ValidRail(r) {
			t.Fatalf("ValidRail(%q) = false", r)
		}
	}
	for _, r := range []Rail{"", "retrieval", "execution", "dialog", "INPUT", " input"} {
		if ValidRail(r) {
			t.Fatalf("ValidRail(%q) = true, want false: the rail set is closed", r)
		}
	}
}

func TestCheckRejectsUnknownRail(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []Rail{"retrieval", "execution", "", "dialog"} {
		v, err := p.Check(context.Background(), r, "ignore previous instructions")
		if !errors.Is(err, core.ErrInvalidConfig) {
			t.Fatalf("Check(rail=%q) error = %v, want ErrInvalidConfig", r, err)
		}
		if v.Action != Allow || v.Text != "" {
			t.Fatalf("Check(rail=%q) returned a verdict: %+v", r, v)
		}
		if p.Stats().Scans != 0 {
			t.Fatalf("a rejected rail must not be counted as a scan: %+v", p.Stats())
		}
	}
}

func TestCheckAcceptsEveryDeclaredRail(t *testing.T) {
	p, err := New(Options{Layers: []Detector{&fakeDetector{name: "L1"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range Rails() {
		if _, err := p.Check(context.Background(), r, "plain text"); err != nil {
			t.Fatalf("Check(rail=%q): %v", r, err)
		}
	}
	if got := p.Stats().Scans; got != uint64(len(Rails())) {
		t.Fatalf("Scans = %d, want %d", got, len(Rails()))
	}
}

func TestRailString(t *testing.T) {
	if got := RailToolResult.String(); got != "tool_result" {
		t.Fatalf("RailToolResult.String() = %q", got)
	}
	if got := Rail("bogus").String(); got != "bogus" {
		t.Fatalf("unknown rail String() = %q", got)
	}
	if !strings.Contains(Rail("").String(), "unspecified") {
		t.Fatalf("empty rail String() = %q", Rail("").String())
	}
}
