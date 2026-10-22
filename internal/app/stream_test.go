package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/KirillYuzh/Gobellim/model"
)

type fakeStreamer struct {
	mu     sync.Mutex
	queue  []model.Response
	deltas [][]string
	calls  int
}

func (f *fakeStreamer) Model() string { return "fake-stream" }

func (f *fakeStreamer) Chat(ctx context.Context, req model.Request) (model.Response, error) {
	return f.ChatStream(ctx, req, func(string) error { return nil })
}

func (f *fakeStreamer) ChatStream(_ context.Context, _ model.Request, onDelta func(string) error) (model.Response, error) {
	f.mu.Lock()
	if len(f.queue) == 0 {
		f.mu.Unlock()
		return model.Response{}, errors.New("fakeStreamer: no more scripted responses")
	}
	resp := f.queue[0]
	deltas := f.deltas[0]
	f.queue = f.queue[1:]
	f.deltas = f.deltas[1:]
	f.calls++
	f.mu.Unlock()

	for _, d := range deltas {
		if err := onDelta(d); err != nil {
			return model.Response{}, err
		}
	}
	return resp, nil
}

func newFakeStreamer(text string) *fakeStreamer {
	return &fakeStreamer{
		queue: []model.Response{{
			Message:      model.Message{Role: model.RoleAssistant, Content: text},
			Usage:        model.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30},
			FinishReason: model.FinishStop,
		}},
		deltas: [][]string{splitRunes(text, 40)},
	}
}

func splitRunes(s string, n int) []string {
	runes := []rune(s)
	out := make([]string, 0, len(runes)/n+1)
	for i := 0; i < len(runes); i += n {
		end := i + n
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

func drain(t *testing.T, ch <-chan StreamChunk) (string, model.Message, int) {
	t.Helper()
	var streamed strings.Builder
	var final model.Message
	var textChunks int
	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("stream error: %v", chunk.Err)
		}
		if chunk.Retract {
			t.Fatal("unexpected retract")
		}
		if chunk.Text != "" {
			textChunks++
			streamed.WriteString(chunk.Text)
		}
		if chunk.Done {
			final = chunk.Final
		}
	}
	return streamed.String(), final, textChunks
}

func TestChatStreamWindowsAndFinal(t *testing.T) {
	text := strings.Repeat("Hello world. This is a streamed answer. ", 40)
	app := newRealApp(t, discardES{}, newFakeStreamer(text), nil, testPolicyMemory, nil)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	ch, err := app.ChatStream(ctx, ChatRequest{
		System:   "be brief",
		Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	streamed, final, chunks := drain(t, ch)

	if chunks < 2 {
		t.Fatalf("a long answer must arrive in several windows, got %d text chunks", chunks)
	}
	if final.Content == "" {
		t.Fatal("the done chunk must carry the final message")
	}
	if streamed != final.Content {
		t.Fatalf("streamed %d chars, final %d chars", len(streamed), len(final.Content))
	}
	if final.Role != model.RoleAssistant {
		t.Fatalf("final role = %q", final.Role)
	}
}

func TestChatStreamFallsBackWhenNotStreamer(t *testing.T) {
	llm := &scripted{t: t, queue: []model.Response{textResponse("buffered answer")}}
	app := newRealApp(t, discardES{}, llm, nil, testPolicyMemory, nil)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	ch, err := app.ChatStream(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	streamed, final, chunks := drain(t, ch)

	if chunks != 1 || streamed != "buffered answer" {
		t.Fatalf("fallback streamed %q in %d chunks", streamed, chunks)
	}
	if final.Content != "buffered answer" {
		t.Fatalf("final = %q", final.Content)
	}
}

func TestChatStreamMasksPII(t *testing.T) {
	filler := strings.Repeat("alpha bravo charlie delta echo foxtrot. ", 20)
	text := filler + "SNILS 112-233-445 95 end. " + filler
	app := newRealApp(t, discardES{}, newFakeStreamer(text), nil, testPolicyMemory, nil)

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Roles: []string{"admin"}, Tenant: "t"})
	ch, err := app.ChatStream(ctx, ChatRequest{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	streamed, final, _ := drain(t, ch)

	if strings.Contains(streamed, "112-233-445 95") {
		t.Fatalf("the raw SNILS leaked into the stream: %q", streamed)
	}
	if !strings.Contains(streamed, "[PII:SNILS") {
		t.Fatalf("the SNILS must be masked in the stream: %q", streamed)
	}
	if final.Content != streamed {
		t.Fatal("the final message must match the emitted stream")
	}
}

func TestChatStreamTruncatedInputBlocksEgress(t *testing.T) {
	type sendIn struct {
		To string `json:"to"`
	}

	streamer := &fakeStreamer{
		queue:  []model.Response{toolCallResponse("send_mail", `{"to":"a@example.com"}`)},
		deltas: [][]string{{""}},
	}
	sent := false
	app := newRealApp(t, discardES{}, streamer, nil, testPolicyMemory, func(c *Config) {
		c.GuardMaxChars = 16
		c.MaxSteps = 3
	}, Tool("send_mail",
		func(context.Context, sendIn) (string, error) { sent = true; return "sent", nil },
		AnyHost()))

	ctx := WithPrincipal(context.Background(), Principal{UserID: "u", Tenant: "t", Roles: []string{"admin"}})
	ch, err := app.ChatStream(ctx, ChatRequest{Messages: []model.Message{
		{Role: model.RoleUser, Content: strings.Repeat("очень длинный пользовательский ввод ", 40)},
	}})
	if err != nil {
		// The input rail rejects the truncated turn before streaming starts.
		if !errors.Is(err, ErrGuardReject) {
			t.Fatalf("err = %v, want ErrGuardReject", err)
		}
	} else {
		var streamErr error
		for chunk := range ch {
			if chunk.Err != nil {
				streamErr = chunk.Err
			}
		}
		if !errors.Is(streamErr, ErrGuardReject) {
			t.Fatalf("err = %v, want ErrGuardReject: a truncated input is rejected before any tool runs", streamErr)
		}
	}
	if sent {
		t.Fatal("egress must not run on a turn whose input the guard could not finish scanning")
	}
}
