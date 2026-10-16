package kit

import (
	"context"
	"testing"

	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/model"
)

type regionalLLM struct{ region model.Region }

func (regionalLLM) Chat(context.Context, model.Request) (model.Response, error) {
	return model.Response{}, nil
}

func (regionalLLM) Model() string { return "regional" }

func (l regionalLLM) Region() model.Region { return l.region }

func regionSubs() []guard.Substitution {
	return []guard.Substitution{{Placeholder: "[PII:phone]", Category: guard.CatPII, Value: "+7 999 888-77-66"}}
}

// TestRegionOutsideRUDisablesAllowPII: a provider that declares a region other
// than RegionRU makes PII masking mandatory, so the AllowPII relaxation is
// refused and the tool only ever sees the placeholder (BR-106) — the same
// fail-closed path BR-04 already defines.
func TestRegionOutsideRUDisablesAllowPII(t *testing.T) {
	var got string
	app, tn := piiApp(t, piiTool(t, "send_sms", true, &got), regionSubs())
	app.cfg.LLM = regionalLLM{region: model.RegionEU}

	if _, err := app.execTool(WithPrincipal(context.Background(), tn.principal),
		model.ToolCall{Name: "send_sms", Args: `{"phone":"[PII:phone]"}`}, tn); err != nil {
		t.Fatal(err)
	}
	if got != "[PII:phone]" {
		t.Fatalf("a provider outside RegionRU must only see the placeholder, got %q", got)
	}
}

func TestRegionRUKeepsAllowPII(t *testing.T) {
	var got string
	app, tn := piiApp(t, piiTool(t, "send_sms", true, &got), regionSubs())
	app.cfg.LLM = regionalLLM{region: model.RegionRU}

	if _, err := app.execTool(WithPrincipal(context.Background(), tn.principal),
		model.ToolCall{Name: "send_sms", Args: `{"phone":"[PII:phone]"}`}, tn); err != nil {
		t.Fatal(err)
	}
	if got != "+7 999 888-77-66" {
		t.Fatalf("a RegionRU provider must still honour AllowPII, got %q", got)
	}
}

func TestPiiMaskingRequiredOnlyForADeclaredForeignRegion(t *testing.T) {
	for _, tc := range []struct {
		name string
		llm  model.Client
		want bool
	}{
		{"no region capability", &fakeLLM{}, false},
		{"ru", regionalLLM{region: model.RegionRU}, false},
		{"empty", regionalLLM{region: ""}, false},
		{"eu", regionalLLM{region: model.RegionEU}, true},
		{"us", regionalLLM{region: model.RegionUS}, true},
	} {
		if got := piiMaskingRequired(tc.llm); got != tc.want {
			t.Fatalf("%s: piiMaskingRequired = %v, want %v", tc.name, got, tc.want)
		}
	}
}
