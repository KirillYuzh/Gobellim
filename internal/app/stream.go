package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/KirillYuzh/Gobellim/authz"
	"github.com/KirillYuzh/Gobellim/guard"
	"github.com/KirillYuzh/Gobellim/internal/call"
	"github.com/KirillYuzh/Gobellim/internal/core"
	"github.com/KirillYuzh/Gobellim/internal/id"
	"github.com/KirillYuzh/Gobellim/model"
	"github.com/KirillYuzh/Gobellim/trace"
)

const (
	streamMinWindow = 400
	streamHoldback  = 96
)

// StreamChunk is one stream event: guard-approved Text, a Retract signal, a
// terminal Done carrying the assembled Final message, or an Err; every chunk
// carries the turn's TraceID.
type StreamChunk struct {
	Text    string
	TraceID string
	Retract bool
	Done    bool
	Final   model.Message
	Err     error
}

var errStreamGuarded = errors.New("gobellim: stream blocked by guard")

// ChatStream is the streaming form of Chat: it runs the same guard, tool, RBAC,
// memory, and budget path but emits only text approved by the output rail. It
// requires the chat permission, returns a channel the caller must drain until it
// closes, and falls back to a single buffered chunk when the LLM client does not
// implement model.Streamer.
func (a *App) ChatStream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	ao := a.beginAudit(ctx, "chat")
	pr, err := principal(ctx)
	if err != nil {
		ao.end(err)
		return nil, err
	}
	// Authorize before guard, memory and recall: a principal without `chat`
	// must be stopped here (BR-09).
	if err := a.policy.Authorize(pr, authz.PermChat); err != nil {
		ao.end(err)
		return nil, err
	}
	if err := a.rateLimit(pr); err != nil {
		ao.end(err)
		return nil, err
	}
	client := sanitizeMessages(req.Messages)
	if len(client) == 0 {
		err := fmt.Errorf("gobellim: chat stream: no user or assistant messages: %w", core.ErrInvalidArgs)
		ao.end(err)
		return nil, err
	}
	rawInput := client[len(client)-1].Content

	ch := make(chan StreamChunk, 8)
	streamer, ok := a.cfg.LLM.(model.Streamer)
	if !ok {
		go func() {
			defer close(ch)
			defer ao.end(nil)
			res, cerr := a.Chat(ctx, req)
			if cerr != nil {
				select {
				case ch <- StreamChunk{Err: cerr, TraceID: res.TraceID}:
				case <-ctx.Done():
				}
				return
			}
			select {
			case ch <- StreamChunk{Text: res.Message.Content, TraceID: res.TraceID}:
			case <-ctx.Done():
				return
			}
			select {
			case ch <- StreamChunk{Done: true, Final: res.Message, TraceID: res.TraceID}:
			case <-ctx.Done():
			}
		}()
		return ch, nil
	}

	traceID := id.New()
	ao.trace(traceID)
	t := &turn{
		traceID:      traceID,
		principal:    pr,
		callsPerTool: make(map[string]int),
		approved:     make(map[string]bool),
	}
	for i := range client {
		v, gerr := a.guardTextVerdict(ctx, guard.RailInput, "guard:input", client[i].Content, traceID)
		if gerr != nil {
			ao.end(gerr)
			return nil, gerr
		}
		if v.Action != guard.Allow || v.Truncated {
			t.markUntrusted()
		}
		t.subs = append(t.subs, v.Substitutions...)
		client[i].Content = v.Text
	}
	// rawInput (captured before the loop) is the original client text.

	go a.streamTurn(ctx, req, pr, client, t, streamer, ch, ao, rawInput)
	return ch, nil
}

func (a *App) streamTurn(ctx context.Context, req ChatRequest, pr core.Principal, client []model.Message, t *turn, streamer model.Streamer, ch chan<- StreamChunk, ao *auditOp, rawInput string) {
	defer close(ch)
	traceID := t.traceID
	var streamErr error
	sendErr := func(err error) {
		if streamErr == nil {
			streamErr = err
		}
		select {
		case ch <- StreamChunk{TraceID: traceID, Err: err}:
		case <-ctx.Done():
		}
	}
	defer func() { ao.end(streamErr) }()
	var total Usage
	var out model.Message

	ctx, span, tr := a.startOperation(ctx, pr, "chat", traceID, req.Tags)
	defer span.End()
	defer func() {
		tr.Output = out.Content
		tr.Usage = Usage{
			PromptTokens:     total.PromptTokens,
			CompletionTokens: total.CompletionTokens,
			TotalTokens:      total.TotalTokens,
			Cost:             total.Cost,
		}
		a.applyTrace(span, tr)
	}()

	system := req.System
	if a.spotlightEnabled() {
		system = joinSystem(system, spotlightReminder)
	}
	msgs := make([]model.Message, 0, len(client)+2)
	if req.ThreadID != "" {
		history, historyUntrusted, herr := a.memoryLoad(ctx, pr, req.ThreadID, a.memoryLimit(), traceID)
		if herr != nil {
			sendErr(herr)
			return
		}
		if historyUntrusted {
			t.markUntrusted()
		}
		if a.memoryRecall() > 0 && a.cfg.Embedder != nil {
			hits, rerr := a.memoryRecallHits(ctx, pr, client[len(client)-1].Content, a.memoryRecall(), traceID)
			if rerr != nil {
				a.logger().Warn("gobellim: chat: memory recall failed", "err", rerr)
			} else {
				if block := memoryRecallBlock(hits); block != "" {
					system = joinSystem(system, block)
				}
				for _, h := range hits {
					if h.Untrusted {
						t.markUntrusted()
					}
				}
			}
		}
		if system != "" {
			msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: system})
		}
		msgs = append(msgs, history...)
	} else if system != "" {
		msgs = append(msgs, model.Message{Role: model.RoleSystem, Content: system})
	}
	msgs = append(msgs, client...)
	tr.Input = a.recordedInput(client[len(client)-1].Content, rawInput)

	specs := a.allowedToolSpecs(pr, client[len(client)-1].Content)

	for step := 0; step < a.cfg.MaxSteps; step++ {
		toolChoice := req.ToolChoice
		if step == a.cfg.MaxSteps-1 && len(specs) > 0 {
			toolChoice = "none"
		}
		mreq := model.Request{
			Messages:    msgs,
			Tools:       specs,
			Temperature: req.Temperature,
			MaxTokens:   req.MaxTokens,
			ToolChoice:  toolChoice,
		}
		cacheKey := a.responseCacheKey(mreq)

		var emitted strings.Builder
		wg := &windowGuard{
			app: a, ctx: ctx, rail: guard.RailOutput, traceID: traceID,
			emit: func(s string) error {
				emitted.WriteString(s)
				select {
				case ch <- StreamChunk{TraceID: traceID, Text: s}:
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			},
		}

		resp, cerr := call.Do(ctx, a.calls, call.Req{
			Kind: call.KindLLM, Name: a.cfg.LLM.Model(), Model: a.cfg.LLM.Model(),
			Permission: authz.PermChat, TraceID: traceID,
		}, func(ctx context.Context, span trace.Span) (model.Response, error) {
			span.SetAttributes(
				trace.String(trace.AttrKind, KindGeneration),
				trace.Model(a.cfg.LLM.Model()),
				trace.Operation("chat"),
			)
			if hit, ok := a.cached(cacheKey); ok {
				span.SetAttributes(
					trace.String(trace.AttrCache, "hit"),
					trace.FinishReason(string(hit.FinishReason)),
				)
				span.SetTokens(hit.Usage.PromptTokens, hit.Usage.CompletionTokens)
				a.recordOutput(span, hit.Message.Content)
				if err := wg.Write(hit.Message.Content); err != nil {
					return model.Response{}, err
				}
				return hit, nil
			}
			r, rerr := streamer.ChatStream(ctx, mreq, wg.Write)
			if rerr != nil {
				return model.Response{}, rerr
			}
			cost := a.cfg.Pricing.Cost(r.Usage)
			span.SetAttributes(trace.FinishReason(string(r.FinishReason)))
			span.SetTokens(r.Usage.PromptTokens, r.Usage.CompletionTokens)
			if cost > 0 {
				span.SetAttributes(trace.Float64(trace.AttrCost, cost))
			}
			a.recordOutput(span, r.Message.Content)
			a.remember(cacheKey, r)
			return r, nil
		})
		if cerr != nil {
			if errors.Is(cerr, errStreamGuarded) {
				ch <- StreamChunk{TraceID: traceID, Retract: true}
			}
			sendErr(cerr)
			return
		}

		total.PromptTokens += resp.Usage.PromptTokens
		total.CompletionTokens += resp.Usage.CompletionTokens
		total.TotalTokens += resp.Usage.TotalTokens
		total.Cost += a.cfg.Pricing.Cost(resp.Usage)
		if a.cfg.MaxTotalTokens > 0 && total.TotalTokens > a.cfg.MaxTotalTokens {
			sendErr(fmt.Errorf("gobellim: chat: token budget %d exceeded (%d): %w",
				a.cfg.MaxTotalTokens, total.TotalTokens, core.ErrBudget))
			return
		}

		if len(resp.Message.ToolCalls) == 0 {
			if err := wg.Close(); err != nil {
				if errors.Is(err, errStreamGuarded) {
					select {
					case ch <- StreamChunk{TraceID: traceID, Retract: true}:
					case <-ctx.Done():
					}
				}
				sendErr(err)
				return
			}
			out = resp.Message
			out.Content = emitted.String()
			if req.ThreadID != "" {
				turnMsgs := make([]model.Message, 0, len(client)+1)
				turnMsgs = append(turnMsgs, client...)
				turnMsgs = append(turnMsgs, out)
				if aerr := a.memoryAppend(ctx, pr, req.ThreadID, turnMsgs, t.isUntrusted(), traceID); aerr != nil {
					a.logger().Warn("gobellim: chat: memory append failed", "err", aerr)
				}
			}
			select {
			case ch <- StreamChunk{TraceID: traceID, Done: true, Final: out}:
			case <-ctx.Done():
			}
			return
		}

		msgs = append(msgs, resp.Message)
		results, terr := a.runToolCalls(ctx, resp.Message.ToolCalls, t)
		if terr != nil {
			sendErr(terr)
			return
		}
		msgs = append(msgs, results...)
	}
	sendErr(fmt.Errorf("gobellim: chat: reached max_steps=%d: %w", a.cfg.MaxSteps, core.ErrMaxSteps))
}

type windowGuard struct {
	app     *App
	ctx     context.Context
	rail    guard.Rail
	traceID string
	buf     []rune
	emit    func(string) error
}

// Write buffers delta and flushes guard-approved windows to the downstream
// writer.
func (g *windowGuard) Write(delta string) error {
	g.buf = append(g.buf, []rune(delta)...)
	return g.flush(false)
}

// Close flushes the remaining buffer through the output guard.
func (g *windowGuard) Close() error { return g.flush(true) }

func (g *windowGuard) flush(final bool) error {
	if len(g.buf) == 0 {
		return nil
	}
	cut := len(g.buf)
	if !final {
		if len(g.buf) <= streamMinWindow {
			return nil
		}
		cut = boundaryCut(g.buf, streamHoldback)
		if cut <= 0 {
			return nil
		}
	}
	window := string(g.buf[:cut])
	rest := g.buf[cut:]

	v, err := call.Guard(g.ctx, g.app.calls, call.Req{
		Kind: call.KindGuard, Name: "guard:stream", Rail: g.rail, Text: window, TraceID: g.traceID,
	})
	if err != nil {
		return err
	}
	if v.Action == guard.Reject {
		return errStreamGuarded
	}
	if err := g.emit(v.Text); err != nil {
		return err
	}
	g.buf = append([]rune(nil), rest...)
	return nil
}

func boundaryCut(runes []rune, holdback int) int {
	n := len(runes) - holdback
	if n <= 0 {
		return 0
	}
	for i := n - 1; i > 0; i-- {
		switch runes[i] {
		case '\n', '.', '!', '?', ';', ':':
			return i + 1
		}
	}
	return n
}
