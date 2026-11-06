// Notebook 03: a sequential flow with a foreign approval boundary.
// Run: go run ./examples/notebooks/03_flow_mock
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/examples/notebooks/notebook"
	"github.com/KirillYuzh/Gobellim/flow"
	"github.com/KirillYuzh/Gobellim/testkit"
)

type refundCase struct {
	OrderID  string
	Cents    int
	Executed bool
}

func main() {
	ctx := context.Background()
	requester := gobellim.Principal{UserID: "u-1", Tenant: "acme"}
	approver := gobellim.Principal{UserID: "m-1", Tenant: "acme"}

	notebook.Cell("1. Store и состояние", `
Store делает Resume одноразовым: nonce занимается атомарно. Suspension и state
хранит приложение; никакого bearer-токена нет. Без Store одноразовость
работает только в процессе.`)
	wf, err := flow.New[refundCase]("refund",
		flow.WithAuthorizer(testkit.AllowAll),
		flow.WithStore(flow.NewMemStore()),
		flow.WithAudit(testkit.DiscardAudit()),
		flow.WithTTL(time.Hour),
	)
	if err != nil {
		panic(err)
	}

	notebook.Cell("2. Шаги и граница одобрения", `
prepare вычисляет состояние. Approve не выполняет действие: он останавливает
flow и выпускает карточку. execute запускается только после проверенного Resume.`)
	wf.Step("prepare", func(_ context.Context, _ gobellim.Principal, s *refundCase) error {
		s.OrderID = "A-1042"
		s.Cents = 4900
		return nil
	})
	wf.Approve("manager", flow.ApproveConfig[refundCase]{
		Permission:       "refund:approve",
		ActionPermission: "orders:refund",
		FourEyes:         true,
		TTL:              time.Hour,
		Card: func(_ context.Context, _ gobellim.Principal, s refundCase) (gobellim.ApprovalCard, error) {
			args, err := json.Marshal(map[string]any{"order_id": s.OrderID, "cents": s.Cents})
			if err != nil {
				return gobellim.ApprovalCard{}, err
			}
			return gobellim.ApprovalCard{Tool: "orders.refund", Operation: gobellim.Write, Args: args}, nil
		},
		Target: func(_ context.Context, p gobellim.Principal, s refundCase) (gobellim.Resource, error) {
			return gobellim.Resource{Type: "order", ID: s.OrderID, TenantID: p.Tenant}, nil
		},
	})
	wf.Step("execute", func(_ context.Context, _ gobellim.Principal, s *refundCase) error {
		s.Executed = true
		return nil
	})

	notebook.Cell("3. Run → Suspended", `
Run проходит prepare и останавливается на границе manager.`)
	res, err := wf.Run(ctx, requester, refundCase{})
	if err != nil {
		panic(err)
	}
	notebook.Result("status: %v", res.Status)
	notebook.Result("executed before approval: %v", res.State.Executed)
	notebook.Result("card: %s / %s / digest=%s", res.Suspension.Card.Tool, res.Suspension.Card.Operation, res.Suspension.Card.Digest)

	notebook.Cell("4. Resume → Done", `
Апрувер m-1 отличается от requester u-1 (Four-Eyes), права проверены,
действие не изменилось, nonce занят один раз.`)
	out, err := wf.Resume(ctx, approver, *res.Suspension, res.State, flow.Approve)
	if err != nil {
		panic(err)
	}
	notebook.Result("status: %v, executed: %v", out.Status, out.State.Executed)

	notebook.Cell("5. Повторный Resume → ErrReplay", `
Тот же токен второй раз не выполнит шаг: Store уже занял nonce.`)
	_, err = wf.Resume(ctx, approver, *res.Suspension, res.State, flow.Approve)
	notebook.Result("replay detected: %v (errors.Is(ErrReplay)=%v)", err, errors.Is(err, flow.ErrReplay))

	fmt.Println()
}
