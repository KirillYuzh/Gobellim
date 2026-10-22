// Package session builds resumable, typed scenarios for human-in-the-loop
// flows.
//
// A Session declares questions, steps and branches over a typed state. When a
// question has no answer yet, the scenario suspends with a typed question. The
// session stores the run in a Store; the caller shows the question to the user
// and calls Resume with the answer. The library owns the run lifecycle: the
// model never chooses a branch and never sees the run id.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"some-project/internal/workflow"
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
	// ErrConflict means another writer changed the run between Load and Save.
	// The retry loads the new state; nothing was overwritten.
	ErrConflict = errors.New("session: run changed")
)

// Principal owns a run. Ownership is checked on every resume, field by field:
// an empty ChatID matches only another empty ChatID.
type Principal struct {
	UserID string `json:"user_id"`
	Tenant string `json:"tenant"`
	ChatID string `json:"chat_id,omitempty"`
}

// matches reports whether other may act on a run owned by p.
func (p Principal) matches(other Principal) bool {
	return p.UserID == other.UserID && p.Tenant == other.Tenant && p.ChatID == other.ChatID
}

// Question is the typed payload of a suspension: what to ask and how to answer.
type Question struct {
	Stage   string   `json:"stage"`
	Prompt  string   `json:"prompt"`
	Input   Input    `json:"input"`
	Options []string `json:"options,omitempty"`
}

// Result is what Start and Resume return. State is a shallow copy of the
// scenario state at that point: pointers and slices inside S are shared with
// the run, so a caller must not mutate them.
type Result[S any] struct {
	Status   Status    `json:"status"`
	RunID    string    `json:"run_id"`
	Question *Question `json:"question,omitempty"`
	State    S         `json:"state"`
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
	asks  map[string]*ask[S]
	store Store[S]
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
	return &Session[S]{name: name, asks: make(map[string]*ask[S]), store: NewMemoryStore[S]()}
}

// WithStore replaces the run store. The default is a fresh MemoryStore; a nil
// store keeps it.
func (s *Session[S]) WithStore(store Store[S]) *Session[S] {
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
	if a.stage != "" {
		if _, dup := s.asks[a.stage]; dup {
			s.fail(fmt.Errorf("%w: %s: question %q is declared twice", ErrInvalid, s.name, a.stage))
			return s
		}
		s.asks[a.stage] = a
	}
	s.steps = append(s.steps, step[S]{ask: a})
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
	if route == "" {
		return s.fail(fmt.Errorf("%w: %s: Case route is required", ErrInvalid, s.name))
	}
	if _, dup := s.last.cases[route]; dup {
		return s.fail(fmt.Errorf("%w: %s: Case %q is declared twice", ErrInvalid, s.name, route))
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
	id, err := newRunID()
	if err != nil {
		return Result[S]{}, err
	}
	flow, err := s.compile()
	if err != nil {
		return Result[S]{}, err
	}
	exec, err := flow.Start(ctx, st)
	if err != nil {
		return Result[S]{}, err
	}
	run := &Run[S]{ID: id, Principal: principal, Expires: s.store.Expiry()}
	return s.finish(ctx, run, exec, "")
}

// Resume continues a suspended run with the answer of its owner. A repeated
// ResumeID returns the result stored by the previous successful Resume.
func (s *Session[S]) Resume(ctx context.Context, req ResumeRequest) (Result[S], error) {
	run, err := s.store.Load(ctx, req.RunID, req.Principal)
	if err != nil {
		return Result[S]{}, err
	}

	run.mu.Lock()
	defer run.mu.Unlock()

	if req.ResumeID != "" && req.ResumeID == run.ResumeID {
		return run.Last, nil
	}
	if run.Last.Status == StatusCompleted {
		return Result[S]{}, fmt.Errorf("%w: run %s is finished", ErrInvalid, req.RunID)
	}
	pending, err := s.pending(run)
	if err != nil {
		return Result[S]{}, err
	}
	answer, err := pending.answer(req)
	if err != nil {
		return Result[S]{}, err
	}
	exec, err := s.restore(run, pending)
	if err != nil {
		return Result[S]{}, err
	}
	err = exec.Resume(ctx, func(st *S) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return pending.apply(st, answer)
	})
	if err != nil {
		if !exec.Failed() {
			// Nothing was applied: a canceled request or a rejected answer
			// leaves the run waiting for the same question.
			return Result[S]{}, err
		}
		// The answer is applied and the rest of the scenario cannot run. Finish
		// the run so a retry gets a definite error instead of a run that is
		// neither waiting nor complete. The save ignores request cancellation:
		// a dropped connection must not resurrect an already applied answer.
		run.Last = Result[S]{Status: StatusCompleted, RunID: run.ID, State: *exec.State()}
		if saveErr := s.store.Save(context.WithoutCancel(ctx), run); saveErr != nil {
			return Result[S]{}, errors.Join(err, saveErr)
		}
		return Result[S]{}, err
	}
	return s.finish(ctx, run, exec, req.ResumeID)
}

// pending returns the declaration of the question the run waits on.
func (s *Session[S]) pending(run *Run[S]) (*ask[S], error) {
	q := run.Last.Question
	if q == nil {
		return nil, fmt.Errorf("%w: run %s is not waiting for an answer", ErrInvalid, run.ID)
	}
	a, ok := s.asks[q.Stage]
	if !ok {
		return nil, fmt.Errorf("%w: run %s: question %q is not declared", ErrInvalid, run.ID, q.Stage)
	}
	return a, nil
}

// restore rebuilds the execution at the pending question from the stored state.
// It works on a copy: a failed resume must not mutate the stored run.
func (s *Session[S]) restore(run *Run[S], pending *ask[S]) (*workflow.Execution[S], error) {
	flow, err := s.compile()
	if err != nil {
		return nil, err
	}
	st := run.Last.State
	return flow.Position(&st, pending.stage, pending)
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
func (s *Session[S]) finish(ctx context.Context, run *Run[S], exec *workflow.Execution[S], resumeID string) (Result[S], error) {
	var out Result[S]
	if susp := exec.Suspension(); susp != nil {
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
			State: *exec.State(),
		}
	} else {
		out = Result[S]{Status: StatusCompleted, RunID: run.ID, State: *exec.State()}
	}
	run.Last = out
	run.ResumeID = resumeID
	// A completed transition is persisted even if the client is gone: the next
	// request must see it, not repeat it.
	if err := s.store.Save(context.WithoutCancel(ctx), run); err != nil {
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
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return flow, nil
}
