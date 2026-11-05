// Notebook 05: a real GigaChat free-tier task with Langfuse audit.
// Run:
//
//	export GIGACHAT_AUTH_KEY='<Authorization key from GigaChat Studio>'
//	go run ./examples/notebooks/05_gigachat_task
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

type askIn struct {
	TenantID string `gobellim:"source=principal,required"`
	Question string `gobellim:"required"`
}

type askOut struct {
	Answer string `gobellim:"required"`
}

func main() {
	ctx := context.Background()
	if !notebook.NeedEnv("GIGACHAT_AUTH_KEY") {
		return
	}

	notebook.Cell("1. GigaChat free tier", `
Authorization key из GigaChat Studio обменивается на access token.
Scope по умолчанию GIGACHAT_API_PERS.
Если TLS падает из-за российского корневого сертификата, задайте
GIGACHAT_INSECURE_TLS=1 (только для локального демо).`)
	model, err := gigachat.New(gigachat.Config{
		AuthKey:     os.Getenv("GIGACHAT_AUTH_KEY"),
		InsecureTLS: os.Getenv("GIGACHAT_INSECURE_TLS") == "1",
	})
	if err != nil {
		panic(err)
	}

	notebook.Cell("2. Langfuse", `
С реальными LANGFUSE_PUBLIC_KEY и LANGFUSE_SECRET_KEY события уходят в
Langfuse. Без них sink работает в dry-run и ничего не печатает.`)
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

	engine, err := gobellim.New(
		gobellim.WithModel(model),
		gobellim.WithAuthorizer(testkit.AllowAll),
		gobellim.WithAudit(sink),
	)
	if err != nil {
		panic(err)
	}
	task, err := gobellim.NewTask[askIn, askOut](engine, gobellim.TaskConfig{
		Name:        "gigachat.answer",
		Instruction: "Answer in Russian, briefly. Return JSON with the Answer field.",
		MaxTokens:   300,
	})
	if err != nil {
		panic(err)
	}

	notebook.Cell("3. Run", `
Task отправляет system prompt, вход и JSON schema. GigaChat не гарантирует
schema, поэтому адаптер добавляет инструкцию, а core всё равно декодирует
ответ строго и проверяет поля.`)
	res, err := task.Run(ctx, gobellim.Principal{UserID: "u-1", Tenant: "acme"},
		askIn{TenantID: "acme", Question: "Что такое возврат товара? Ответь одним предложением."})
	if err != nil {
		notebook.Result("ошибка: %v", err)
		fmt.Println("Проверьте Authorization key, доступ к GigaChat и TLS-настройки.")
		return
	}
	notebook.Result("answer: %s", res.Output.Answer)
	notebook.Result("model calls: %d, tokens: %d", res.ModelCalls, res.Usage.TotalTokens)

	notebook.Cell("Итог", `
Вызов GigaChat идёт тем же путём, что и любой другой Model: Task, лимиты,
audit, Langfuse. Без tools Gate разрешает только финальный ответ.`)
	fmt.Println()
}
