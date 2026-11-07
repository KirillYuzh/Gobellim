package gobellim

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type schemaInput struct {
	TenantID string `gobellim:"source=principal,required"`
	TicketID string `gobellim:"source=application,required"`
	Body     string `gobellim:"required,sensitivity=patterns"`
	Topic    string `gobellim:"oneof=billing|technical"`
	Count    int    `gobellim:"min=1,max=5"`
}

func TestSchemaBuild(t *testing.T) {
	s, err := newStructSchema(reflect.TypeOf(schemaInput{}))
	if err != nil {
		t.Fatal(err)
	}
	var node map[string]any
	if err := json.Unmarshal(s.raw, &node); err != nil {
		t.Fatal(err)
	}
	props, ok := node["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema has no properties")
	}
	topic, ok := props["Topic"].(map[string]any)
	if !ok {
		t.Fatal("schema has no Topic")
	}
	enum, ok := topic["enum"].([]any)
	if !ok || len(enum) != 2 {
		t.Fatalf("Topic enum = %v", topic["enum"])
	}
	required, _ := node["required"].([]any)
	if len(required) != 3 {
		t.Fatalf("required = %v, want 3 fields", required)
	}
}

func TestSchemaRejectsUnknownTag(t *testing.T) {
	type bad struct {
		X string `gobellim:"nope"`
	}
	_, err := newStructSchema(reflect.TypeOf(bad{}))
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("err = %v, want ErrInvalidConfig", err)
	}
}

func TestSchemaRejectsUnsupportedType(t *testing.T) {
	type bad struct {
		M map[string]string
	}
	_, err := newStructSchema(reflect.TypeOf(bad{}))
	if !errors.Is(err, ErrUnsupportedType) {
		t.Fatalf("err = %v, want ErrUnsupportedType", err)
	}
}

func TestDecodeStrict(t *testing.T) {
	s, err := newStructSchema(reflect.TypeOf(schemaInput{}))
	if err != nil {
		t.Fatal(err)
	}

	good := `{"TenantID":"acme","TicketID":"t1","Body":"hello","Topic":"billing","Count":2}`
	v, err := decodeStrict[schemaInput]([]byte(good), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.validate(reflect.ValueOf(v)); err != nil {
		t.Fatal(err)
	}

	cases := map[string]string{
		"unknown field": `{"TenantID":"acme","TicketID":"t1","Body":"hello","Extras":1}`,
		"duplicate key": `{"TenantID":"acme","TenantID":"other","TicketID":"t1","Body":"hello"}`,
		"trailing data": `{"TenantID":"acme","TicketID":"t1","Body":"hello"} {}`,
		"null":          `null`,
		"empty":         ``,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeStrict[schemaInput]([]byte(raw), 1024); !errors.Is(err, ErrInvalidArgs) {
				t.Fatalf("err = %v, want ErrInvalidArgs", err)
			}
		})
	}
}

func TestValidateConstraints(t *testing.T) {
	s, err := newStructSchema(reflect.TypeOf(schemaInput{}))
	if err != nil {
		t.Fatal(err)
	}
	base := schemaInput{TenantID: "acme", TicketID: "t1", Body: "hello", Topic: "billing", Count: 2}

	badTopic := base
	badTopic.Topic = "other"
	if err := s.validate(reflect.ValueOf(badTopic)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("topic err = %v", err)
	}

	badCount := base
	badCount.Count = 9
	if err := s.validate(reflect.ValueOf(badCount)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("count err = %v", err)
	}

	missing := base
	missing.Body = ""
	if err := s.validate(reflect.ValueOf(missing)); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("required err = %v", err)
	}
}

func TestPrepareInputMasksAndLabels(t *testing.T) {
	s, err := newStructSchema(reflect.TypeOf(schemaInput{}))
	if err != nil {
		t.Fatal(err)
	}
	p := Principal{UserID: "u-1", Tenant: "acme"}
	hs := newHandleSet()
	in := schemaInput{
		TenantID: "acme",
		TicketID: "t1",
		Body:     "write to alice@example.com",
		Topic:    "billing",
		Count:    2,
	}
	masked, labels, seen, err := s.prepareInput(in, p, hs)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := masked["Body"].(string)
	if strings.Contains(body, "alice@example.com") {
		t.Fatalf("patterns value was not masked: %q", body)
	}
	if !strings.Contains(body, "<h") {
		t.Fatalf("masked body has no handle: %q", body)
	}
	if labels["TenantID"].Integrity != Trusted {
		t.Fatalf("TenantID integrity = %v, want Trusted", labels["TenantID"].Integrity)
	}
	if labels["Body"].Sensitivity != Patterns {
		t.Fatalf("Body sensitivity = %v, want patterns", labels["Body"].Sensitivity)
	}
	if seen.Sensitivity != Patterns {
		t.Fatalf("input label sensitivity = %v, want patterns", seen.Sensitivity)
	}

	in.TenantID = "other"
	if _, _, _, err := s.prepareInput(in, p, hs); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("principal mismatch err = %v", err)
	}
}

func TestTrustedSecretBecomesTrustedHandle(t *testing.T) {
	type in struct {
		OrderID string `gobellim:"source=application,sensitivity=secret,required"`
	}
	s, err := newStructSchema(reflect.TypeOf(in{}))
	if err != nil {
		t.Fatal(err)
	}
	hs := newHandleSet()
	p := Principal{UserID: "u-1", Tenant: "acme"}
	masked, labels, seen, err := s.prepareInput(in{OrderID: "A-1042"}, p, hs)
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := masked["OrderID"].(string)
	if !isHandleRef(ref) {
		t.Fatalf("OrderID = %q, want a handle", ref)
	}
	entry, ok := hs.get(ref)
	if !ok {
		t.Fatal("handle was not registered")
	}
	if entry.value != "A-1042" {
		t.Fatalf("handle value = %q", entry.value)
	}
	if entry.label.Integrity != Trusted {
		t.Fatalf("handle integrity = %v, want Trusted", entry.label.Integrity)
	}
	if labels["OrderID"].Integrity != Trusted {
		t.Fatalf("field integrity = %v, want Trusted", labels["OrderID"].Integrity)
	}
	// A trusted secret still marks the answer Secret: the model saw a handle, but
	// the answer can paste the value back in a resolve field. The answer itself is
	// never Trusted, because a model wrote it.
	if seen.Integrity != Derived || seen.Sensitivity != Secret {
		t.Fatalf("input label = %+v, want derived/secret", seen)
	}
}

func TestResolveHandles(t *testing.T) {
	hs := newHandleSet()
	ref := hs.put("A-1042", Label{Integrity: Trusted})
	out, labels, err := resolveHandles([]byte(`{"OrderID":"`+ref+`"}`), hs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "A-1042") {
		t.Fatalf("resolved = %s", out)
	}
	if labels["OrderID"].Integrity != Trusted {
		t.Fatalf("label = %v", labels["OrderID"].Integrity)
	}
	if _, _, err := resolveHandles([]byte(`{"OrderID":"<h999>"}`), hs); !errors.Is(err, ErrUngrounded) {
		t.Fatalf("fabricated handle err = %v, want ErrUngrounded", err)
	}
}

func FuzzDecodeStrict(f *testing.F) {
	f.Add([]byte(`{"TenantID":"acme","TicketID":"t1","Body":"hello"}`))
	f.Add([]byte(`{"a":`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = decodeStrict[schemaInput](raw, 1<<10)
	})
}

func FuzzResolveHandles(f *testing.F) {
	f.Add([]byte(`{"OrderID":"<h1>"}`))
	f.Add([]byte(`[[[[`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		hs := newHandleSet()
		hs.put("A-1042", Label{Integrity: Trusted})
		_, _, _ = resolveHandles(raw, hs)
	})
}

func FuzzParseTag(f *testing.F) {
	f.Add("required,sensitivity=secret,ground=OrderID")
	f.Add("oneof=a|b,min=1,max=2")
	f.Add("untrusted")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		_, _ = parseTag(raw)
	})
}

// FuzzRedactResult asserts the one guarantee the redactor sells: a value tagged
// secret never reaches the model in clear, whatever bytes it contains.
func FuzzRedactResult(f *testing.F) {
	f.Add("TOP-SECRET")
	f.Add("")
	f.Add("\u0000\u0001")
	f.Fuzz(func(t *testing.T, s string) {
		type res struct {
			S string            `gobellim:"sensitivity=secret"`
			M map[string]string `gobellim:"sensitivity=secret"`
			B []byte            `gobellim:"sensitivity=secret"`
		}
		hs := newHandleSet()
		redacted, _ := redactResult(res{S: s, M: map[string]string{"k": s}, B: []byte(s)}, "t", hs)
		body, err := marshalNoEscape(redacted)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if s == "" {
			return // empty values stay empty; there is nothing to leak
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("unmarshal %s: %v", body, err)
		}
		if v, ok := out["S"].(string); !ok || !isHandleRef(v) {
			t.Fatalf("S = %v, want a handle; body %s", out["S"], body)
		}
		m, _ := out["M"].(map[string]any)
		if v, ok := m["k"].(string); !ok || !isHandleRef(v) {
			t.Fatalf("M[k] = %v, want a handle; body %s", m["k"], body)
		}
		if v, ok := out["B"].(string); !ok || !isHandleRef(v) {
			t.Fatalf("B = %v, want a handle; body %s", out["B"], body)
		}
	})
}

func TestSchemaRejectsUnsupportedCommonTypes(t *testing.T) {
	type withTime struct {
		When time.Time
	}
	type withBytes struct {
		Raw []byte
	}
	type Embedded struct {
		ID string
	}
	type withEmbedded struct {
		Embedded
	}
	for name, typ := range map[string]reflect.Type{
		"time.Time":       reflect.TypeOf(withTime{}),
		"[]byte":          reflect.TypeOf(withBytes{}),
		"embedded struct": reflect.TypeOf(withEmbedded{}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newStructSchema(typ); !errors.Is(err, ErrUnsupportedType) {
				t.Fatalf("err = %v, want ErrUnsupportedType", err)
			}
		})
	}
}

func TestHandleDedupeKeepsLabelsApart(t *testing.T) {
	hs := newHandleSet()
	trusted := Label{Integrity: Trusted, Sensitivity: Secret, Sources: []SourceRef{{Kind: "application"}}}
	untrusted := Label{Integrity: Untrusted, Sensitivity: Secret, Sources: []SourceRef{{Kind: "input"}}}
	a := hs.put("same", trusted)
	b := hs.put("same", untrusted)
	if a == b {
		t.Fatalf("an untrusted value reused a trusted handle %s", a)
	}
	entry, ok := hs.get(b)
	if !ok || entry.label.Integrity != Untrusted {
		t.Fatalf("label = %+v", entry.label)
	}
}
