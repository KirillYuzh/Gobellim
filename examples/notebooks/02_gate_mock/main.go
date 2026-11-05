// Notebook 02: every model-initiated call goes through the Gate.
// Run: go run ./examples/notebooks/02_gate_mock
package main

import (
	"context"
	"fmt"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type gateIn struct {
	TenantID string `gobellim:"source=principal,required"`
	Body     string `gobellim:"required,sensitivity=pii"`
	OrderID  string `gobellim:"sensitivity=secret"`
}

type out struct {
	Reply string
}

type readArgs struct {
	OrderID string `gobellim:"required,ground"`
}

type readResult struct {
	OK bool
}

func main() {
	ctx := context.Background()
	principal := gobellim.Principal{UserID: "u-1", Tenant: "acme"}
	input := gateIn{TenantID: "acme", Body: "hello", OrderID: "A-100"}

	notebook.Cell("Вход", `
OrderID помечен sensitivity=secret, поэтому модель видит handle <h1>.
Дальше один и тот же вход прогоняется через разные инструменты и решения.`)

	readTool := gobellim.Tool[readArgs, readResult]{
		Name:       "orders.read",
		Effect:     gobellim.Read,
		Permission: "orders:read",
		Resource: func(p gobellim.Principal, a readArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			fmt.Println("   ↳ tool Run executed")
			return readResult{OK: true}, nil
		},
	}

	notebook.Cell("1. Неизвестный инструмент", `
Модель просит инструмент, которого нет в закрытом реестре. Это Deny.`)
	runCase(ctx, principal, input, "unknown tool", nil, "phantom", map[string]any{})

	notebook.Cell("2. Сырой ID вместо handle", `
Поле OrderID помечено ground: рантайм принимает только handle, созданный для
этого запуска. Строка "A-999" от модели — Deny.`)
	runCase(ctx, principal, input, "raw grounded arg", []gobellim.ToolDef{readTool}, "orders.read",
		map[string]any{"OrderID": "A-999"})

	notebook.Cell("3. Handle из входа", `
Модель возвращает <h1> — существующий handle. Значение резолвится, Read
разрешён, tool handler выполняется.`)
	runCase(ctx, principal, input, "handle grounded arg", []gobellim.ToolDef{readTool}, "orders.read",
		map[string]any{"OrderID": "<h1>"})

	notebook.Cell("4. Write без аргументов", `
У инструмента нет полей аргументов, поэтому модель не может его «испортить».
Но это не значит «доверено»: пустой набор меток = Untrusted, Write → Escalate.`)
	wipe := gobellim.Tool[struct{}, readResult]{
		Name:   "danger.wipe",
		Effect: gobellim.Write,
		Run: func(context.Context, gobellim.Principal, struct{}) (readResult, error) {
			fmt.Println("   ↳ tool Run executed")
			return readResult{}, nil
		},
	}
	runCase(ctx, principal, input, "empty-args write", []gobellim.ToolDef{wipe}, "danger.wipe", map[string]any{})

	notebook.Cell("5. Чужой tenant", `
Resource привязан к другому tenant. Gate проверяет это до Authorizer и до
tool handler.`)
	foreign := gobellim.Tool[readArgs, readResult]{
		Name:       "orders.read.foreign",
		Effect:     gobellim.Read,
		Permission: "orders:read",
		Resource: func(gobellim.Principal, readArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: "A-1", TenantID: "other-tenant"}
		},
		Run: func(context.Context, gobellim.Principal, readArgs) (readResult, error) {
			fmt.Println("   ↳ tool Run executed")
			return readResult{}, nil
		},
	}
	runCase(ctx, principal, input, "tenant mismatch", []gobellim.ToolDef{foreign}, "orders.read.foreign",
		map[string]any{"OrderID": "<h1>"})

	notebook.Cell("Итог", `
Deny и Escalate — это разные вещи. Deny не выполняет действие и не даёт
человеку его одобрить. Escalate останавливает Task и отдаёт карточку.`)
	fmt.Println()
}

func runCase(ctx context.Context, p gobellim.Principal, input gateIn, label string, tools []gobellim.ToolDef, callName string, args map[string]any) {
	model := testkit.NewFakeModel(
		testkit.Call(callName, args),
		testkit.Reply(`{"Reply":"ok"}`),
	)
	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
	)
	if err != nil {
		panic(err)
	}
	task, err := gobellim.NewTask[gateIn, out](engine, gobellim.TaskConfig{
		Name:        "gate.demo",
		Instruction: "Call the tool if needed.",
		Tools:       tools,
	})
	if err != nil {
		panic(err)
	}
	res, err := task.Run(ctx, p, input)
	if err != nil {
		notebook.Result("%s: error %v", label, err)
		return
	}
	if len(res.ToolCalls) == 0 {
		notebook.Result("%s: no tool call; status=%v", label, res.Status)
		return
	}
	d := res.ToolCalls[0].Decision
	notebook.Result("%s: %s %v %q", label, d.Kind, d.RuleIDs, d.Reason)
}
