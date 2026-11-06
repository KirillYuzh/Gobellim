// Notebook 06: GigaChat proposes a write; the Gate escalates to a trusted approver.
// Run:
//
//	export GIGACHAT_AUTH_KEY='<Authorization key from GigaChat Studio>'
//	go run ./examples/notebooks/06_gigachat_approval
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/plugins/gigachat"
	"github.com/KirillYuzh/Gobellim/plugins/langfuse"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type refundIn struct {
	TenantID string `gobellim:"source=principal,required"`
	OrderID  string `gobellim:"sensitivity=secret,required"`
	Request  string `gobellim:"required,sensitivity=patterns"`
}

type refundOut struct {
	Answer string
}

type refundArgs struct {
	OrderID string `gobellim:"required,ground"`
	Cents   int    `gobellim:"min=1,max=5000"`
}

type refundResult struct {
	RefundID string
	Status   string
}

func main() {
	ctx := context.Background()
	if !notebook.NeedEnv("GIGACHAT_AUTH_KEY") {
		return
	}

	model, err := gigachat.New(gigachat.Config{
		AuthKey:     os.Getenv("GIGACHAT_AUTH_KEY"),
		InsecureTLS: os.Getenv("GIGACHAT_INSECURE_TLS") == "1",
	})
	if err != nil {
		panic(err)
	}

	sink, err := langfuse.New(langfuse.Config{
		Host:          os.Getenv("LANGFUSE_HOST"),
		PublicKey:     os.Getenv("LANGFUSE_PUBLIC_KEY"),
		SecretKey:     os.Getenv("LANGFUSE_SECRET_KEY"),
		DryRun:        os.Getenv("LANGFUSE_PUBLIC_KEY") == "" || os.Getenv("LANGFUSE_SECRET_KEY") == "",
		Writer:        io.Discard,
		BatchSize:     8,
		FlushInterval: time.Second,
	})
	if err != nil {
		panic(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sink.Close(closeCtx)
	}()

	var executed int
	refund := gobellim.Tool[refundArgs, refundResult]{
		Name:       "orders.refund",
		Operation:  gobellim.Write,
		Permission: "orders:refund",
		Target: func(p gobellim.Principal, a refundArgs) gobellim.Resource {
			return gobellim.Resource{Type: "order", ID: a.OrderID, TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, refundArgs) (refundResult, error) {
			executed++
			return refundResult{RefundID: "demo-refund", Status: "ok"}, nil
		},
	}

	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(sink),
	)
	if err != nil {
		panic(err)
	}
	task, err := gobellim.NewTask[refundIn, refundOut](engine, gobellim.TaskConfig{
		Name: "gigachat.refund",
		Instruction: "If the user asks for a refund, call orders.refund with the OrderID exactly as it appears in " +
			"the input (it may look like <h1>) and the amount in Cents. Otherwise return JSON with the Answer field.",
		Tools:     []gobellim.ToolDef{refund},
		MaxTokens: 400,
	})
	if err != nil {
		panic(err)
	}

	notebook.Cell("1. Вход", `
OrderID помечен sensitivity=secret: модель видит handle <h1>.
Request помечен sensitivity=patterns: e-mail/телефоны маскируются.
Приложение передаёт настоящий OrderID только через handle set.`)
	input := refundIn{
		TenantID: "acme",
		OrderID:  "A-1042",
		Request:  "Верните 4900 за заказ, пожалуйста.",
	}
	notebook.Result("input prepared: OrderID=%q Request=%q", input.OrderID, input.Request)

	notebook.Cell("2. GigaChat предлагает действие", `
Модель возвращает имя инструмента и JSON-аргументы. Это только предложение:
runtime ищет инструмент в закрытом реестре и отправляет аргументы в Gate.`)
	res, err := task.Run(ctx, gobellim.Principal{UserID: "u-1", Tenant: "acme"}, input)
	if err != nil {
		notebook.Result("ошибка: %v", err)
		return
	}

	notebook.Result("status: %v, tool calls: %d", res.Status, len(res.ToolCalls))
	for _, call := range res.ToolCalls {
		notebook.Result("tool %s: %s %v", call.Name, call.Decision.Kind, call.Decision.RuleIDs)
	}
	notebook.Result("tool executed: %d", executed)

	if res.Pending != nil {
		notebook.Cell("3. Approval card", `
Write с аргументом из untrusted-поля не выполняется. Карточка содержит
резолвнутые аргументы для апрувера, digest действия и provenance.
В реальном сервисе карточка уходит в UI; приложение применяет ACL апрувера.`)
		notebook.Result("tool: %s, operation: %s", res.Pending.Card.Tool, res.Pending.Card.Operation)
		notebook.Result("args: %s", string(res.Pending.Card.Args))
		notebook.Result("digest: %s", res.Pending.Card.Digest)
		for _, p := range res.Pending.Card.Provenance {
			notebook.Result("provenance: %s ← %s (%s)", p.Arg, p.Source, p.Integrity)
		}

		notebook.Cell("4. Доверенный источник одобрил", `
Приложение вызывает ExecuteApproved только после реального одобрения и
передаёт Principal запросившего, а не апрувера. Gate заново проверяет tenant,
RBAC/ACL и бизнес-валидацию; аргументы должны совпасть с digest карточки.`)
		_, rec, err := task.ExecuteApproved(ctx, gobellim.Principal{UserID: "u-1", Tenant: "acme"}, gobellim.Principal{UserID: "m-1", Tenant: "acme"}, res.Pending)
		if err != nil {
			notebook.Result("execute: error %v", err)
		} else {
			notebook.Result("execute: decision=%v executed=%v, tool runs=%d", rec.Decision.Kind, rec.Executed, executed)
		}
	} else {
		notebook.Cell("3. Модель не вызвала инструмент", `
Иногда модель сразу отвечает текстом. Это нормально: Gate не выполняет
действие, которого не было.`)
		notebook.Result("answer: %s", res.Output.Answer)
	}

	notebook.Cell("Итог", `
Один и тот же путь: GigaChat → Gate → (Allow | Escalate | Deny) → audit.
Langfuse получает trace, generation и spans с решениями Gate, без prompt text.`)
	fmt.Println()
}
