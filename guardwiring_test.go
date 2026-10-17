package kit

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/trace"
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

const digestA = "1111111111111111111111111111111111111111111111111111111111111111"

func TestGuardVersionNamesTheLayers(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2"}
	})
	if v, want := app.GuardVersion(), app.PolicyHash()+"/L1+l2"; v != want {
		t.Fatalf("GuardVersion() = %q, want %q", v, want)
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

func TestGuardVersionHasNoArtifactSegmentWithoutOne(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil)
	if v := app.GuardVersion(); v != app.PolicyHash()+"/L1" {
		t.Fatalf("GuardVersion() = %q, want %q", v, app.PolicyHash()+"/L1")
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

// TestGuardFailOpenIsPerRail: the only degradation knob is Config.GuardFailOpen,
// a per-rail list. A rail outside it stays fail-closed.
func TestGuardFailOpenIsPerRail(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2-offline", err: errDetectorOffline}
		c.GuardFailOpen = []guard.Rail{guard.RailInput}
	})
	ctx := context.Background()

	v, err := app.Guard().Check(ctx, guard.RailInput, "hello")
	if err != nil {
		t.Fatalf("a fail-open rail must tolerate the failure: %v", err)
	}
	if !v.Degraded || !strings.Contains(v.DegradedReason, "l2-offline") {
		t.Fatalf("verdict = %+v, want degraded with the failing layer named", v)
	}
	if v.Action != guard.Allow {
		t.Fatalf("action = %v, want allow: L1 found nothing", v.Action)
	}

	for _, rail := range []guard.Rail{guard.RailOutput, guard.RailToolResult} {
		if _, err := app.Guard().Check(ctx, rail, "hello"); !errors.Is(err, errDetectorOffline) {
			t.Fatalf("rail %s outside the policy err = %v, want the layer failure", rail, err)
		}
	}
	if h := app.Health(); !h.Degraded || len(h.GuardFailOpen) != 1 || h.GuardFailOpen[0] != "input" {
		t.Fatalf("health = %+v, want the per-rail policy reported", h)
	}
}

func TestGuardFailOpenOnEveryListedRail(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, func(c *Config) {
		c.GuardL2 = stubDetector{name: "l2-offline", err: errDetectorOffline}
		c.GuardFailOpen = guard.Rails()
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
	if h := app.Health(); len(h.GuardFailOpen) != len(guard.Rails()) {
		t.Fatalf("health = %+v, want every fail-open rail reported", h)
	}
}

func TestGuardRejectsAnUnknownFailOpenRail(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	_, err := New(context.Background(), Config{
		ES: discardES{}, LLM: &fakeLLM{}, Policy: policy,
		GuardL2:       guard.L1Only(),
		GuardFailOpen: []guard.Rail{"sideways"},
	})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig for an unknown rail", err)
	}
}

// TestNewRequiresAnExplicitGuardLayerChoice: a nil Config.GuardL2 no longer
// means "L1 only" by accident — the weaker composition must be requested with
// guard.L1Only() (D80).
func TestNewRequiresAnExplicitGuardLayerChoice(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	_, err := New(context.Background(), Config{ES: discardES{}, LLM: &fakeLLM{}, Policy: policy})
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig: a nil GuardL2 must be refused", err)
	}
}

func TestL1OnlyIsReportedInHealth(t *testing.T) {
	app := newRealApp(t, discardES{}, &fakeLLM{}, nil, testPolicyMemory, nil)
	h := app.Health()
	if !h.GuardL1Only {
		t.Fatal("Health().GuardL1Only must be true for guard.L1Only()")
	}
	if len(h.GuardLayers) != 1 || h.GuardLayers[0] != "L1" {
		t.Fatalf("GuardLayers = %v, want [L1]", h.GuardLayers)
	}
}

func TestL1OnlyWarnsAtStartup(t *testing.T) {
	policy := parseTestPolicy(t, testPolicyMemory)
	logs := &syncBuffer{}
	app, err := New(context.Background(), Config{
		ES: discardES{}, LLM: &fakeLLM{}, Policy: policy, GuardL2: guard.L1Only(),
		Logger: slog.New(slog.NewTextHandler(logs, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = app.Close(context.Background()) }()
	if !strings.Contains(logs.String(), "L1 only") {
		t.Fatalf("no L1-only warning logged: %q", logs.String())
	}
}
