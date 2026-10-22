package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type state struct {
	Param  string
	Path   string
	Topic  string
	Result string
}

func newScenario(store Store[state]) *Session[state] {
	s := New[state]("research")
	if store != nil {
		s.WithStore(store)
	}
	return s.
		AskText("param", "Which parameter?", func(st *state, a string) error {
			st.Param = a
			return nil
		}).
		AskChoice("path", "Which path?", []string{"A", "B"}, func(st *state, a string) error {
			st.Path = a
			return nil
		}).
		Branch(func(st *state) string { return st.Path }).
		Case("A", func(f *Session[state]) {
			f.Do("finish", func(_ context.Context, st *state) error {
				st.Result = "A:" + st.Param
				return nil
			})
		}).
		Case("B", func(f *Session[state]) {
			f.AskChoice("topic", "Which topic?", []string{"X", "Y"}, func(st *state, a string) error {
				st.Topic = a
				return nil
			}).
				Do("finish", func(_ context.Context, st *state) error {
					st.Result = "B:" + st.Topic
					return nil
				})
		})
}

var owner = Principal{UserID: "u-1", Tenant: "acme"}

func startScenario(t *testing.T, s *Session[state]) Result[state] {
	t.Helper()
	res, err := s.Start(context.Background(), owner, &state{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return res
}

func resumeScenario(t *testing.T, s *Session[state], req ResumeRequest) Result[state] {
	t.Helper()
	req.Principal = owner
	res, err := s.Resume(context.Background(), req)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	return res
}

func TestSessionAsksAndCompletes(t *testing.T) {
	s := newScenario(nil)

	first := startScenario(t, s)
	if first.Status != StatusAwaitingUser || first.Question == nil || first.RunID == "" {
		t.Fatalf("start = %+v", first)
	}
	if first.Question.Stage != "param" || first.Question.Input != InputText {
		t.Fatalf("question = %+v", first.Question)
	}

	second := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "go modules"})
	if second.Status != StatusAwaitingUser || second.Question == nil || second.Question.Stage != "path" {
		t.Fatalf("second = %+v", second)
	}
	if second.Question.Input != InputChoice || len(second.Question.Options) != 2 {
		t.Fatalf("question = %+v", second.Question)
	}
	if second.State.Param != "go modules" {
		t.Fatalf("state = %+v, want the answer applied", second.State)
	}

	done := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Choice: "A"})
	if done.Status != StatusCompleted || done.Question != nil || done.State.Result != "A:go modules" {
		t.Fatalf("done = %+v", done)
	}
}

// TestSessionRestoredByAnotherSession resumes a run through a second Session
// over the same store: the execution is rebuilt from the stored state, as it
// would be after a process restart.
func TestSessionRestoredByAnotherSession(t *testing.T) {
	store := NewMemoryStore[state]()
	first := newScenario(store)
	start := startScenario(t, first)

	second := newScenario(store)
	resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Text: "go modules"})
	done := resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Choice: "B"})
	if done.Status != StatusAwaitingUser || done.Question.Stage != "topic" {
		t.Fatalf("branch = %+v", done)
	}
	done = resumeScenario(t, second, ResumeRequest{RunID: start.RunID, Choice: "Y"})
	if done.Status != StatusCompleted || done.State.Result != "B:Y" || done.State.Param != "go modules" {
		t.Fatalf("done = %+v", done)
	}
}

func TestSessionBranchB(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)
	resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "go modules"})
	branch := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Choice: "B"})
	if branch.Status != StatusAwaitingUser || branch.Question.Stage != "topic" {
		t.Fatalf("branch = %+v", branch)
	}
	done := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Choice: "Y"})
	if done.Status != StatusCompleted || done.State.Result != "B:Y" {
		t.Fatalf("done = %+v", done)
	}
}

func TestSessionRejectsBadChoice(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)
	resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "p"})

	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Choice: "C",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	// Отказ не ломает запуск: правильный ответ всё ещё проходит.
	done := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Choice: "A"})
	if done.Status != StatusCompleted {
		t.Fatalf("done = %+v", done)
	}
}

func TestSessionRequiresText(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "   ",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestSessionRejectsForeignRun(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)

	foreign := Principal{UserID: "u-2", Tenant: "acme"}
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: foreign, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: Principal{UserID: "u-1", Tenant: "other"}, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for another tenant", err)
	}
}

// TestSessionRejectsForeignChat pins the documented ChatID rule: an omitted
// ChatID does not mean "any chat".
func TestSessionRejectsForeignChat(t *testing.T) {
	s := newScenario(nil)
	chat := Principal{UserID: "u-1", Tenant: "acme", ChatID: "chat-1"}
	first, err := s.Start(context.Background(), chat, &state{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound when ChatID is omitted", err)
	}
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: Principal{UserID: "u-1", Tenant: "acme", ChatID: "chat-2"}, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound for another chat", err)
	}
}

func TestSessionUnknownRun(t *testing.T) {
	s := newScenario(nil)
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: "run-nope", Principal: owner, Choice: "A",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSessionResumeIsIdempotent(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)
	req := ResumeRequest{RunID: first.RunID, Principal: owner, ResumeID: "r1", Text: "p"}

	a := resumeScenario(t, s, req)
	b := resumeScenario(t, s, req)
	if a != b {
		t.Fatalf("repeated resume returned %+v, want %+v", b, a)
	}
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, ResumeID: "r2", Choice: "A",
	}); err != nil {
		t.Fatalf("a new answer after the repeated one must work: %v", err)
	}
}

func TestSessionFinishedRunCannotBeResumed(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)
	resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "p"})
	resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Choice: "A"})

	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Choice: "B",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

// TestSessionStepErrorFinishesRun: an error after the answer is applied must
// not leave a run that is neither waiting nor complete.
func TestSessionStepErrorFinishesRun(t *testing.T) {
	boom := errors.New("boom")
	s := New[state]("boom").
		AskText("param", "Which parameter?", func(st *state, a string) error {
			st.Param = a
			return nil
		}).
		Do("fail", func(context.Context, *state) error { return boom })

	first := startScenario(t, s)
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	_, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "finished") {
		t.Fatalf("retry err = %v, want ErrInvalid with a finished run", err)
	}
}

// TestSessionCanceledResumeKeepsRun: a canceled request applies nothing and
// leaves the run waiting for the same answer.
func TestSessionCanceledResumeKeepsRun(t *testing.T) {
	s := newScenario(nil)
	first := startScenario(t, s)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Resume(ctx, ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	done := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "p"})
	if done.Status != StatusAwaitingUser || done.State.Param != "p" {
		t.Fatalf("resume after cancel = %+v, want the answer applied", done)
	}
}

// TestSessionApplyPanicKeepsRun: the question callback is isolated like a step,
// and the run stays resumable.
func TestSessionApplyPanicKeepsRun(t *testing.T) {
	panicked := false
	s := New[state]("panic").
		AskText("param", "Which parameter?", func(st *state, a string) error {
			if !panicked {
				panicked = true
				panic("apply boom")
			}
			st.Param = a
			return nil
		})

	first := startScenario(t, s)
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); err == nil || !strings.Contains(err.Error(), "panic: apply boom") {
		t.Fatalf("err = %v, want the recovered panic", err)
	}
	done := resumeScenario(t, s, ResumeRequest{RunID: first.RunID, Text: "p"})
	if done.Status != StatusCompleted || done.State.Param != "p" {
		t.Fatalf("retry = %+v, want the answer applied", done)
	}
}

func TestStoreExpiresRuns(t *testing.T) {
	store := NewMemoryStore[state](TTL(time.Millisecond))
	s := newScenario(store)
	first := startScenario(t, s)

	time.Sleep(5 * time.Millisecond)
	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStoreLimitsRunsPerPrincipal(t *testing.T) {
	store := NewMemoryStore[state](MaxRunsPerPrincipal(1))
	s := newScenario(store)
	first := startScenario(t, s)
	startScenario(t, s)

	if _, err := s.Resume(context.Background(), ResumeRequest{
		RunID: first.RunID, Principal: owner, Text: "p",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want the oldest run evicted", err)
	}
}

func TestSessionRejectsBrokenDeclaration(t *testing.T) {
	cases := map[string]*Session[state]{
		"case without branch": New[state]("x").
			Case("A", func(f *Session[state]) {}),
		"branch without case": New[state]("x").
			Branch(func(*state) string { return "A" }),
		"duplicate stage": New[state]("x").
			AskText("param", "?", func(*state, string) error { return nil }).
			Do("param", func(context.Context, *state) error { return nil }),
		"duplicate stage across branches": New[state]("x").
			Branch(func(*state) string { return "A" }).
			Case("A", func(f *Session[state]) {
				f.AskText("param", "?", func(*state, string) error { return nil })
			}).
			Case("B", func(f *Session[state]) {
				f.AskText("param", "?", func(*state, string) error { return nil })
			}),
		"duplicate case": New[state]("x").
			Branch(func(*state) string { return "A" }).
			Case("A", func(f *Session[state]) {}).
			Case("A", func(f *Session[state]) {}),
	}
	for name, s := range cases {
		if _, err := s.Start(context.Background(), owner, &state{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestSessionRequiresPrincipal(t *testing.T) {
	s := newScenario(nil)
	if _, err := s.Start(context.Background(), Principal{}, &state{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}

func TestAskSkipIf(t *testing.T) {
	s := New[state]("skip").
		AskText("param", "Which parameter?", func(st *state, a string) error {
			st.Param = a
			return nil
		}, SkipIf(func(st *state) bool { return st.Param != "" })).
		Do("finish", func(_ context.Context, st *state) error {
			st.Result = st.Param
			return nil
		})

	res, err := s.Start(context.Background(), owner, &state{Param: "ready"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Status != StatusCompleted || res.State.Result != "ready" {
		t.Fatalf("res = %+v, want an already answered question skipped", res)
	}

	res, err = s.Start(context.Background(), owner, &state{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if res.Status != StatusAwaitingUser || res.Question.Stage != "param" {
		t.Fatalf("res = %+v, want the question asked", res)
	}
}
