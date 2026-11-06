// Notebook 04: the Langfuse sink in dry-run mode.
// Run: go run ./examples/notebooks/04_langfuse_mock
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/plugins/langfuse"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type askIn struct {
	TenantID string `gobellim:"source=principal,required"`
	Question string `gobellim:"required"`
}

type askOut struct {
	Answer string `gobellim:"required"`
}

type searchArgs struct {
	Query string `gobellim:"required"`
}

type searchResult struct {
	Hits []string
}

func main() {
	ctx := context.Background()

	notebook.Cell("1. Sink без сети", `
DryRun печатает ровно тот JSON, который ушёл бы в POST /api/public/ingestion.
В нём нет prompt text, сырых аргументов и секретов — только labels, digest,
decision, latency и токены.`)
	sink, err := langfuse.New(langfuse.Config{
		DryRun:        true,
		Writer:        os.Stdout,
		BatchSize:     8,
		FlushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		panic(err)
	}

	search := gobellim.Tool[searchArgs, searchResult]{
		Name:       "kb.search",
		Access:     gobellim.Read,
		Permission: "kb:search",
		Target: func(p gobellim.Principal, _ searchArgs) gobellim.Resource {
			return gobellim.Resource{Type: "kb", ID: "kb-main", TenantID: p.Tenant}
		},
		Run: func(context.Context, gobellim.Principal, searchArgs) (searchResult, error) {
			return searchResult{Hits: []string{"refund policy v2"}}, nil
		},
	}
	model := testkit.NewFakeModel(
		testkit.Call("kb.search", searchArgs{Query: "refund policy"}),
		testkit.Reply(`{"Answer":"Возврат возможен в течение 14 дней."}`),
	)
	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(sink),
	)
	if err != nil {
		panic(err)
	}
	task, err := gobellim.NewTask[askIn, askOut](engine, gobellim.TaskConfig{
		Name:        "kb.answer",
		Instruction: "Answer from the knowledge base.",
		Tools:       []gobellim.ToolDef{search},
	})
	if err != nil {
		panic(err)
	}

	notebook.Cell("2. Запуск Task", `
События: task.started, model.completed, gate.allowed, tool.completed,
model.completed, task.completed. Sink превращает их в trace / generation /
span.`)
	res, err := task.Run(ctx, gobellim.Principal{UserID: "u-1", Tenant: "acme"},
		askIn{TenantID: "acme", Question: "Можно ли вернуть товар?"})
	if err != nil {
		panic(err)
	}
	notebook.Result("status: %v, answer: %q", res.Status, res.Output.Answer)

	if err := sink.Close(ctx); err != nil {
		panic(err)
	}
	notebook.Result("dropped events: %d", sink.Dropped())
	notebook.Cell("3. Что смотреть в Langfuse", `
trace: run_id, task; principal и tenant — только при SendPrincipalID/SendTenant.
generation: model, prompt/completion/total tokens, latency.
span: gate decision, rule ids, access, integrity, sensitivity, args digest.
Для реальной отправки задайте LANGFUSE_PUBLIC_KEY, LANGFUSE_SECRET_KEY и
LANGFUSE_HOST — см. пример 06.`)
	fmt.Println()
}
