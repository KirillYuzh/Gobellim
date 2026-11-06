package gobellim

import (
	"context"
	"testing"
)

type benchArgs struct {
	OrderID string `gobellim:"required,ground"`
	Note    string
}

type benchResult struct {
	Card  string   `gobellim:"sensitivity=secret"`
	Email string   `gobellim:"sensitivity=pii"`
	Note  string   `gobellim:"sensitivity=secret"`
	Items []string `gobellim:"sensitivity=secret"`
	Plain []string
}

func benchHandles() *handleSet {
	hs := newHandleSet()
	hs.put("A-1042", Label{
		Integrity:   Untrusted,
		Sensitivity: Secret,
		Sources:     []SourceRef{{Kind: "input", ID: "OrderID"}},
	})
	return hs
}

// BenchmarkToolPrepare is the per-tool-call cost of decoding, resolving handles,
// validating and labelling the arguments.
func BenchmarkToolPrepare(b *testing.B) {
	tool := Tool[benchArgs, benchResult]{Name: "orders.read", Operation: OperationRead}
	raw := []byte(`{"OrderID":"<h1>","Note":"hello"}`)
	hs := benchHandles()
	p := Principal{UserID: "u-1", Tenant: "acme"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tool.prepare(context.Background(), p, raw, hs, 8<<10); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGateDecide measures the decision itself, with the arguments already
// prepared.
func BenchmarkGateDecide(b *testing.B) {
	tool := Tool[benchArgs, benchResult]{
		Name:      "orders.read",
		Operation: OperationRead,
		Target: func(p Principal, a benchArgs) Resource {
			return Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
	}
	prep, err := tool.prepare(context.Background(), Principal{UserID: "u-1", Tenant: "acme"},
		[]byte(`{"OrderID":"<h1>","Note":"hello"}`), benchHandles(), 8<<10)
	if err != nil {
		b.Fatal(err)
	}
	prep.spec = tool.toolSpec()
	e := &Engine{authz: AuthorizerFunc(func(context.Context, Principal, string, Resource) (bool, error) {
		return true, nil
	})}
	p := Principal{UserID: "u-1", Tenant: "acme"}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if d := e.decide(context.Background(), p, &prep); d.Kind != Allow {
			b.Fatalf("decision = %v", d)
		}
	}
}

// BenchmarkRedactResult is the cost added to every tool result before it
// re-enters the model context.
func BenchmarkRedactResult(b *testing.B) {
	res := benchResult{
		Card:  "4111 1111 1111 1111",
		Email: "customer@example.com",
		Note:  "note",
		Items: []string{"a", "b", "c"},
		Plain: []string{"a", "b", "c"},
	}
	hs := benchHandles()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := marshalNoEscape(redactResult(res, "orders.read", hs)); err != nil {
			b.Fatal(err)
		}
	}
}
