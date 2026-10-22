// Package session builds resumable, typed scenarios for human-in-the-loop
// flows.
//
// A Session declares questions, steps and branches over a typed state. When a
// question has no answer yet, the scenario suspends with a typed question. The
// caller stores the run, shows the question to the user and calls Resume with
// the answer. The library owns the run lifecycle: the model never chooses a
// branch and never sees the run id.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/KirillYuzh/Gobellim/flow/workflow"
)

// Status is the outcome of Start or Resume.
type Status string

const (
	// StatusAwaitingUser means the scenario suspended on a question.
	StatusAwaitingUser Status = "awaiting_user"
	// StatusCompleted means the scenario reached its end.
	StatusCompleted Status = "completed"
)

// Input is the kind of answer a question expects.
type Input string

const (
	// InputText expects free text in ResumeRequest.Text.
	InputText Input = "text"
	// InputChoice expects one of the question options in ResumeRequest.Choice.
	InputChoice Input = "choice"
)

// Errors reported by Session.
var (
	// ErrNotFound covers both an unknown run and a run of another principal:
	// the library never confirms that a foreign run exists.
	ErrNotFound = errors.New("session: run not found")
	// ErrInvalid marks a malformed scenario or an answer that does not fit the
	// pending question.
	ErrInvalid = errors.New("session: invalid")
)

// Principal owns a run. Ownership is checked on every resume.
type Principal struct {
	UserID string
	Tenant string
	ChatID string
}

// matches reports whether other may act on a run owned by p. An empty ChatID on
// either side means "any chat".
func (p Principal) matches(other Principal) bool {
	if p.UserID != other.UserID || p.Tenant != other.Tenant {
		return false
	}
	return p.ChatID == "" || other.ChatID == "" || p.ChatID == other.ChatID
}

// Question is the typed payload of a suspension: what to ask and how to answer.
type Question struct {
	Stage   string
	Prompt  string
	Input   Input
	Options []string
}

// Result is what Start and Resume return. State is a snapshot of the scenario
// state at that point.
type Result[S any] struct {
	Status   Status
	RunID    string
	Question *Question
	State    S
}

// ResumeRequest continues a run with the answer of its owner.
type ResumeRequest struct {
	RunID     string
	ResumeID  string
	Principal Principal
	Text      string
	Choice    string
}

// Session is a scenario declaration plus the store of its runs.
type Session[S any] struct {
	name  string
	steps []step[S]
	asks  map[string]struct{}
	store *MemoryStore[S]
	last  *branchStep[S]

	once sync.Once
	flow *workflow.Flow[S]
	err  error
}

type step[S any] struct {
	ask    *ask[S]
	do     *doStep[S]
	branch *branchStep[S]
}

type ask[S any] struct {
	stage   string
	prompt  string
	input   Input
	options []string
	skip    func(*S) bool
	apply   func(*S, string) error
}

// AskOption tunes a question.
type AskOption[S any] func(*ask[S])

// SkipIf skips the question when cond reports that the state already has the
// answer. A skipped question does not suspend: the scenario moves on.
func SkipIf[S any](cond func(*S) bool) AskOption[S] {
	return func(a *ask[S]) { a.skip = cond }
}

type doStep[S any] struct {
	name string
	run  func(context.Context, *S) error
}

type branchStep[S any] struct {
	name   string
	decide func(*S) string
	cases  map[string]*Session[S]
}

// New returns an empty scenario with the given name. The name appears in error
// paths.
func New[S any](name string) *Session[S] {
	return &Session[S]{name: name, asks: make(map[string]struct{}), store: NewMemoryStore[S]()}
}

// WithStore replaces the run store. The default is a fresh MemoryStore.
func (s *Session[S]) WithStore(store *MemoryStore[S]) *Session[S] {
	if store != nil {
		s.store = store
	}
	return s
}

// Do appends a deterministic step and returns the session, so calls chain.
func (s *Session[S]) Do(name string, run func(context.Context, *S) error) *Session[S] {
	s.steps = append(s.steps, step[S]{do: &doStep[S]{name: name, run: run}})
	s.last = nil
	return s
}

// AskText appends a question that expects free text.
func (s *Session[S]) AskText(stage, prompt string, apply func(*S, string) error, opts ...AskOption[S]) *Session[S] {
	a := &ask[S]{stage: stage, prompt: prompt, input: InputText, apply: apply}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return s.ask(a)
}

// AskChoice appends a question that expects exactly one of options.
func (s *Session[S]) AskChoice(stage, prompt string, options []string, apply func(*S, string) error, opts ...AskOption[S]) *Session[S] {
	a := &ask[S]{
		stage:   stage,
		prompt:  prompt,
		input:   InputChoice,
		options: append([]string(nil), options...),
		apply:   apply,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(a)
		}
	}
	return s.ask(a)
}

func (s *Session[S]) ask(a *ask[S]) *Session[S] {
	s.steps = append(s.steps, step[S]{ask: a})
	s.asks[a.stage] = struct{}{}
	s.last = nil
	return s
}

// Branch appends a deterministic branch over the state and returns the session.
// The routes are declared by the Case calls that follow.
func (s *Session[S]) Branch(decide func(*S) string) *Session[S] {
	b := &branchStep[S]{
		name:   fmt.Sprintf("branch%d", len(s.steps)),
		decide: decide,
		cases:  map[string]*Session[S]{},
	}
	s.steps = append(s.steps, step[S]{branch: b})
	s.last = b
	return s
}

// Case adds a route to the preceding Branch. build declares the steps of that
// route on a fresh sub-session that shares questions and the store.
func (s *Session[S]) Case(route string, build func(*Session[S])) *Session[S] {
	if s.last == nil {
		return s.fail(fmt.Errorf("%w: %s: Case without Branch", ErrInvalid, s.name))
	}
	if build == nil {
		return s.fail(fmt.Errorf("%w: %s: Case %q has no body", ErrInvalid, s.name, route))
	}
	sub := &Session[S]{name: route, asks: s.asks, store: s.store}
	build(sub)
	if sub.err != nil {
		return s.fail(sub.err)
	}
	s.last.cases[route] = sub
	return s
}

func (s *Session[S]) fail(err error) *Session[S] {
	if s.err == nil {
		s.err = err
	}
	return s
}

// Start compiles the scenario and runs it until it suspends or ends.
func (s *Session[S]) Start(ctx context.Context, principal Principal, st *S) (Result[S], error) {
	if st == nil {
		return Result[S]{}, fmt.Errorf("%w: nil state", ErrInvalid)
	}
	if principal.UserID == "" || principal.Tenant == "" {
		return Result[S]{}, fmt.Errorf("%w: principal user and tenant are required", ErrInvalid)
	}
	flow, err := s.compile()
	if err != nil {
		return Result[S]{}, err
	}
	exec, err := flow.Start(ctx, st)
	if err != nil {
		return Result[S]{}, err
	}
	run := &Run[S]{ID: newRunID(), Principal: principal, Exec: exec, Expires: s.store.expiry()}
	return s.finish(run, "")
}

// Resume continues a suspended run with the answer of its owner. A repeated
// ResumeID returns the stored result instead of applying the answer twice.
func (s *Session[S]) Resume(ctx context.Context, req ResumeRequest) (Result[S], error) {
	run, err := s.store.Load(RunRef{RunID: req.RunID, Principal: req.Principal})
	if err != nil {
		return Result[S]{}, err
	}

	run.mu.Lock()
	defer run.mu.Unlock()

	if req.ResumeID != "" && req.ResumeID == run.resumeID {
		return run.last, nil
	}
	if run.done {
		return Result[S]{}, fmt.Errorf("%w: run %s is finished", ErrInvalid, req.RunID)
	}
	susp := run.Exec.Suspension()
	if susp == nil {
		return Result[S]{}, fmt.Errorf("%w: run %s is not waiting for an answer", ErrInvalid, req.RunID)
	}
	pending, ok := susp.Payload.(*ask[S])
	if !ok {
		return Result[S]{}, fmt.Errorf("%w: run %s has an unexpected question %T", ErrInvalid, req.RunID, susp.Payload)
	}
	answer, err := pending.answer(req)
	if err != nil {
		return Result[S]{}, err
	}
	if err := run.Exec.Resume(ctx, func(st *S) error { return pending.apply(st, answer) }); err != nil {
		return Result[S]{}, err
	}
	return s.finish(run, req.ResumeID)
}

// answer validates the reply against the question contract.
func (a *ask[S]) answer(req ResumeRequest) (string, error) {
	if a.input == InputChoice {
		for _, option := range a.options {
			if req.Choice == option {
				return option, nil
			}
		}
		return "", fmt.Errorf("%w: %s: choice must be one of %v", ErrInvalid, a.stage, a.options)
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return "", fmt.Errorf("%w: %s: text is required", ErrInvalid, a.stage)
	}
	return text, nil
}

// finish stores the run and reports its status to the caller.
func (s *Session[S]) finish(run *Run[S], resumeID string) (Result[S], error) {
	var out Result[S]
	if susp := run.Exec.Suspension(); susp != nil {
		pending, ok := susp.Payload.(*ask[S])
		if !ok {
			return Result[S]{}, fmt.Errorf("%w: %s: unexpected question %T", ErrInvalid, s.name, susp.Payload)
		}
		out = Result[S]{
			Status: StatusAwaitingUser,
			RunID:  run.ID,
			Question: &Question{
				Stage:   pending.stage,
				Prompt:  pending.prompt,
				Input:   pending.input,
				Options: append([]string(nil), pending.options...),
			},
			State: *run.Exec.State(),
		}
	} else {
		run.done = true
		out = Result[S]{Status: StatusCompleted, RunID: run.ID, State: *run.Exec.State()}
	}
	run.last = out
	run.resumeID = resumeID
	if err := s.store.Save(run); err != nil {
		return Result[S]{}, err
	}
	return out, nil
}

// compile builds the workflow flow once and caches the result.
func (s *Session[S]) compile() (*workflow.Flow[S], error) {
	s.once.Do(func() {
		s.flow, s.err = s.build()
	})
	return s.flow, s.err
}

func (s *Session[S]) build() (*workflow.Flow[S], error) {
	if s.err != nil {
		return nil, s.err
	}
	flow := workflow.New[S](s.name)
	seen := make(map[string]struct{}, len(s.steps))
	for _, st := range s.steps {
		switch {
		case st.ask != nil:
			a := st.ask
			if a.stage == "" {
				return nil, fmt.Errorf("%w: %s: question stage is required", ErrInvalid, s.name)
			}
			if a.apply == nil {
				return nil, fmt.Errorf("%w: %s/%s: apply is required", ErrInvalid, s.name, a.stage)
			}
			if a.input == InputChoice && len(a.options) == 0 {
				return nil, fmt.Errorf("%w: %s/%s: choice needs options", ErrInvalid, s.name, a.stage)
			}
			if _, dup := seen[a.stage]; dup {
				return nil, fmt.Errorf("%w: %s: step %q is declared twice", ErrInvalid, s.name, a.stage)
			}
			seen[a.stage] = struct{}{}
			flow.Then(a.stage, func(ctx context.Context, st *S) error {
				if a.skip != nil && a.skip(st) {
					return nil
				}
				return workflow.Suspend(ctx, a)
			})
		case st.do != nil:
			if st.do.name == "" {
				return nil, fmt.Errorf("%w: %s: step name is required", ErrInvalid, s.name)
			}
			if st.do.run == nil {
				return nil, fmt.Errorf("%w: %s/%s: run function is required", ErrInvalid, s.name, st.do.name)
			}
			if _, dup := seen[st.do.name]; dup {
				return nil, fmt.Errorf("%w: %s: step %q is declared twice", ErrInvalid, s.name, st.do.name)
			}
			seen[st.do.name] = struct{}{}
			flow.Then(st.do.name, st.do.run)
		case st.branch != nil:
			b := st.branch
			if b.decide == nil {
				return nil, fmt.Errorf("%w: %s: branch decide function is required", ErrInvalid, s.name)
			}
			if len(b.cases) == 0 {
				return nil, fmt.Errorf("%w: %s: branch has no cases", ErrInvalid, s.name)
			}
			routes := make(map[string]*workflow.Flow[S], len(b.cases))
			for route, sub := range b.cases {
				subFlow, err := sub.build()
				if err != nil {
					return nil, err
				}
				routes[route] = subFlow
			}
			flow.Branch(b.name, b.decide, routes)
		default:
			return nil, fmt.Errorf("%w: %s: empty step", ErrInvalid, s.name)
		}
	}
	if err := flow.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return flow, nil
}
