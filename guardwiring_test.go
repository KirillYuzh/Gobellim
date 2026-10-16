package kit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gobellim/kit/authz"
	"github.com/gobellim/kit/guard"
	"github.com/gobellim/kit/model"
	"github.com/gobellim/kit/trace"
)

type stubDetector struct {
	name string
	find []guard.Finding
	err  error
}

func (d stubDetector) Name() string { return d.name }

func (d stubDetector) Detect(context.Context, string) ([]guard.Finding, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.find, nil
}

func (stubDetector) UsesNaturalText() bool { return true }

type artifactDetector struct {
	stubDetector
	hash string
}

func (d artifactDetector) ArtifactHash() string { return d.hash }

var errDetectorOffline = errors.New("l2 model offline")

func parseTestPolicy(t *testing.T, policyYAML string) *authz.Policy {
	t.Helper()
	policy, err := authz.Parse(strings.NewReader(policyYAML))
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

const (
	digestA = "1111111111111111111111111111111111111111111111111111111111111111"
	digestB = "2222222222222222222222222222222222222222222222222222222222222222"
)

func TestGuardVersionCarriesTheConfiguredArtifact(t *testing.T) {
	a := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2"}
		c.GuardL2ArtifactSHA256 = digestA
	})
	b := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2"}
		c.GuardL2ArtifactSHA256 = digestB
	})

	v := a.GuardVersion()
	t.Logf("guard_version = %s", v)
	if want := a.PolicyHash() + "/L1+l2/" + digestA; v != want {
		t.Fatalf("GuardVersion() = %q, want %q", v, want)
	}
	if !strings.HasSuffix(v, "/"+digestA) {
		t.Fatalf("version %q does not carry the artifact digest", v)
	}
	if a.GuardVersion() == b.GuardVersion() {
		t.Fatalf("swapping the artifact left the version unchanged: %q", v)
	}
}

func TestGuardVersionDerivesTheArtifactFromTheDetector(t *testing.T) {
	det := artifactDetector{stubDetector: stubDetector{name: "l2"}, hash: digestA}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = det
	})

	want := guard.ArtifactHashes([]guard.Detector{det})
	if want == "" {
		t.Fatal("the test detector must report an artifact hash")
	}
	v := app.GuardVersion()
	if !strings.HasSuffix(v, "/"+want) {
		t.Fatalf("GuardVersion() = %q, want the derived artifact %q", v, want)
	}
}

func TestGuardVersionPrefersTheDetectorArtifact(t *testing.T) {
	det := artifactDetector{stubDetector: stubDetector{name: "l2"}, hash: digestA}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = det
		c.GuardL2ArtifactSHA256 = digestB
	})
	if v := app.GuardVersion(); strings.HasSuffix(v, "/"+digestB) {
		t.Fatalf("GuardVersion() = %q, want the artifact the detector reported", v)
	}
}

func TestGuardVersionHasNoArtifactSegmentWithoutOne(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil)
	if v := app.GuardVersion(); v != app.PolicyHash()+"/L1" {
		t.Fatalf("GuardVersion() = %q, want %q", v, app.PolicyHash()+"/L1")
	}
}

func TestGuardRejectsAMalformedArtifactDigest(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	for _, bad := range []string{
		"abc",
		digestA + "00",
		digestA[:63],
		strings.Repeat("A", 64),
		strings.Repeat("z", 64),
		digestA[:63] + " ",
		strings.ToUpper("a" + digestA[1:]),
	} {
		_, err := New(context.Background(), Config{
			ES: discardES{}, LLM: &fakeLLM{}, Policy: policy,
			GuardL2ArtifactSHA256: bad,
		})
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("digest %q: err = %v, want ErrInvalidConfig", bad, err)
		}
	}
}

func TestTruncatedInputIsUntrustedInput(t *testing.T) {
	type sendIn struct {
		To string `json:"to"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("send_mail", `{"to":"a@example.com"}`),
		textResponse("готово"),
	}}
	rec := trace.NewRecorder()
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.GuardMaxChars = 32
		c.MaxSteps = 3
		c.MaxCallsPerTool = 2
		c.Sinks = []trace.Sink{rec}
	})
	sent := false
	if _, err := Tool(app, "send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		Effects(EffectEgress), EgressAny()); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	long := strings.Repeat("привет ", 40)
	_, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: long}}})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized: a truncated turn must forbid egress", err)
	}
	if sent {
		t.Fatal("the egress handler must not run on a turn with an unscanned tail")
	}

	var meta map[string]any
	for _, o := range rec.Snapshot().Observations {
		if o.Name == "guard:input" && o.Metadata["truncated"] == true {
			meta = o.Metadata
		}
	}
	if meta == nil {
		t.Fatal("no guard:input observation carries truncated=true")
	}
	if got := meta["inspected_chars"]; got != 32 {
		t.Fatalf("inspected_chars = %#v, want 32", got)
	}
}

func TestShortInputDoesNotMarkTheTurn(t *testing.T) {
	type sendIn struct {
		To string `json:"to"`
	}
	llm := &scripted{t: t, queue: []model.Response{
		toolCallResponse("send_mail", `{"to":"a@example.com"}`),
		textResponse("готово"),
	}}
	rec := trace.NewRecorder()
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, func(c *Config) {
		c.GuardMaxChars = 32
		c.MaxSteps = 3
		c.MaxCallsPerTool = 2
		c.Sinks = []trace.Sink{rec}
	})
	sent := false
	if _, err := Tool(app, "send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		Effects(EffectEgress), EgressAny()); err != nil {
		t.Fatal(err)
	}

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	if _, err := app.Chat(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: "привет"},
	}}); err != nil {
		t.Fatalf("trusted turn: %v", err)
	}
	if !sent {
		t.Fatal("a fully scanned input must not forbid egress")
	}
	for _, o := range rec.Snapshot().Observations {
		if o.Name == "guard:input" && o.Metadata["truncated"] != nil {
			t.Fatalf("unexpected truncation marker: %+v", o.Metadata)
		}
	}
}

func TestGuardDegradationIsPerRail(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2-offline", err: errDetectorOffline}
		c.GuardDegradedRails = map[string]bool{"input": true}
	})
	ctx := context.Background()

	v, err := app.Guard().Check(ctx, guard.RailInput, "hello")
	if err != nil {
		t.Fatalf("the degraded rail must tolerate the failure: %v", err)
	}
	if !v.Degraded {
		t.Fatal("the verdict must be marked degraded")
	}
	if !strings.Contains(v.DegradedReason, "l2-offline") {
		t.Fatalf("DegradedReason = %q, want the failing layer named", v.DegradedReason)
	}
	if v.Action != guard.Allow {
		t.Fatalf("action = %v, want allow: L1 found nothing", v.Action)
	}

	if _, err := app.Guard().Check(ctx, guard.RailOutput, "hello"); !errors.Is(err, errDetectorOffline) {
		t.Fatalf("a rail outside the policy err = %v, want the layer failure", err)
	}
	if _, err := app.Guard().Check(ctx, guard.RailToolResult, "hello"); !errors.Is(err, errDetectorOffline) {
		t.Fatalf("an unlisted rail err = %v, want the layer failure", err)
	}
	if h := app.Health(); !h.Degraded || len(h.DegradedRails) != 1 || h.DegradedRails[0] != "input" {
		t.Fatalf("health = %+v, want the per-rail policy reported", h)
	}
}

func TestGuardDegradedModeStillDegradesEverywhere(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2-offline", err: errDetectorOffline}
		c.GuardMode = GuardDegraded
	})
	for _, rail := range guard.Rails() {
		v, err := app.Guard().Check(context.Background(), rail, "hello")
		if err != nil {
			t.Fatalf("rail %s: %v", rail, err)
		}
		if !v.Degraded {
			t.Fatalf("rail %s: the verdict must be marked degraded", rail)
		}
	}
}

func TestGuardDegradedRailsCombineWithTheLegacySwitch(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2-offline", err: errDetectorOffline}
		c.GuardMode = GuardDegraded
		c.GuardDegradedRails = map[string]bool{"input": false}
	})
	if _, err := app.Guard().Check(context.Background(), guard.RailOutput, "hello"); err != nil {
		t.Fatalf("GuardDegraded must still degrade every rail: %v", err)
	}
}

func TestGuardRejectsAnUnknownDegradedRail(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	_, err := New(context.Background(), Config{
		ES: discardES{}, LLM: &fakeLLM{}, Policy: policy,
		GuardDegradedRails: map[string]bool{"sideways": true},
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig for an unknown rail", err)
	}
}

func TestGuardEnsembleMergesAndDeduplicatesFindings(t *testing.T) {
	const (
		wide   = "abcdefghij klmnop qrstuv wxyz"
		shadow = "cde"
	)
	detA := stubDetector{name: "det-a", find: []guard.Finding{
		{Category: guard.CatPII, Label: "phone", Start: 0, End: 10},
		{Category: guard.CatPII, Label: "name", Start: 11, End: 17},
	}}
	detB := stubDetector{name: "det-b", find: []guard.Finding{
		{Category: guard.CatPII, Label: "phone", Start: 2, End: 2 + len(shadow)},
		{Category: guard.CatPII, Label: "card", Start: 18, End: 24},
	}}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardEnsembles = []guard.Ensemble{{Name: "L2-ens", Detectors: []guard.Detector{detA, detB}}}
	})

	v, err := app.Guard().Check(context.Background(), guard.RailToolResult, wide)
	if err != nil {
		t.Fatal(err)
	}
	if v.Action != guard.Mask {
		t.Fatalf("action = %v, want mask", v.Action)
	}
	want := []guard.Finding{
		{Category: guard.CatPII, Label: "phone", Start: 0, End: 10},
		{Category: guard.CatPII, Label: "name", Start: 11, End: 17},
		{Category: guard.CatPII, Label: "card", Start: 18, End: 24},
	}
	if len(v.Findings) != len(want) {
		t.Fatalf("findings = %+v, want %+v", v.Findings, want)
	}
	for i, f := range v.Findings {
		if f.Category != want[i].Category || f.Label != want[i].Label ||
			f.Start != want[i].Start || f.End != want[i].End {
			t.Fatalf("finding %d = %+v, want %+v", i, f, want[i])
		}
	}
	if v := app.GuardVersion(); !strings.Contains(v, "L2-ens(det-a+det-b)") {
		t.Fatalf("GuardVersion() = %q, want the ensemble composition", v)
	}
}

func TestGuardL2StillWorksAlongsideAnEnsemble(t *testing.T) {
	l2 := stubDetector{name: "l2"}
	det := stubDetector{name: "det-a"}
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = l2
		c.GuardEnsembles = []guard.Ensemble{{Name: "L2-ens", Detectors: []guard.Detector{det}}}
	})
	if v, want := app.GuardVersion(), app.PolicyHash()+"/L1+l2+L2-ens(det-a)"; v != want {
		t.Fatalf("GuardVersion() = %q, want %q", v, want)
	}
	if _, err := app.Guard().Check(context.Background(), guard.RailInput, "hello"); err != nil {
		t.Fatal(err)
	}
}

func TestGuardRejectsAnEmptyEnsemble(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	_, err := New(context.Background(), Config{
		ES: discardES{}, LLM: &fakeLLM{}, Policy: policy,
		GuardEnsembles: []guard.Ensemble{{Name: "L2-ens"}},
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig for an ensemble without detectors", err)
	}
}
