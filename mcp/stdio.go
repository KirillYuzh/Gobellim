package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KirillYuzh/Gobellim/internal/core"
)

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

const maxMessageBytes = 8 << 20

type stdioTransport struct {
	cfg   Config
	cmd   *exec.Cmd
	stdin io.WriteCloser

	writeMu sync.Mutex

	pendMu  sync.Mutex
	pending map[int64]chan rpcResponse

	nextID atomic.Int64

	done     chan struct{}
	doneOnce sync.Once

	errMu   sync.Mutex
	exitErr error

	closeOnce sync.Once
}

func newStdioTransport(cfg Config) (Transport, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	if cfg.Dir != "" {
		cmd.Dir = cfg.Dir
	}
	if len(cfg.Env) > 0 {
		cmd.Env = append(os.Environ(), cfg.Env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdin pipe: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stdout pipe: %w", cfg.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: stderr pipe: %w", cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: %s: start %s: %w: %w", cfg.Name, cfg.Command, err, core.ErrProvider)
	}

	t := &stdioTransport{
		cfg:     cfg,
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[int64]chan rpcResponse),
		done:    make(chan struct{}),
	}
	go t.readLoop(stdout)
	go t.logStderr(stderr)
	go func() {
		werr := cmd.Wait()
		if werr != nil {
			t.markDone(fmt.Errorf("mcp: %s: server exited: %w: %w", cfg.Name, werr, core.ErrProvider))
			return
		}
		t.markDone(nil)
	}()
	return t, nil
}

func (t *stdioTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := t.nextID.Add(1)
	ch := make(chan rpcResponse, 1)
	t.pendMu.Lock()
	t.pending[id] = ch
	t.pendMu.Unlock()
	defer func() {
		t.pendMu.Lock()
		delete(t.pending, id)
		t.pendMu.Unlock()
	}()

	if err := t.write(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, t.closedErr()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error.unwrap()
		}
		return resp.Result, nil
	}
}

func (t *stdioTransport) Notify(_ context.Context, method string, params any) error {
	return t.write(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		_ = t.stdin.Close()
		select {
		case <-t.done:
		case <-time.After(2 * time.Second):
			_ = t.cmd.Process.Kill()
			<-t.done
		}
	})
	return nil
}

func (t *stdioTransport) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("mcp: %s: encode request: %w", t.cfg.Name, err)
	}
	b = append(b, '\n')

	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	select {
	case <-t.done:
		return t.closedErr()
	default:
	}
	if _, err := t.stdin.Write(b); err != nil {
		return fmt.Errorf("mcp: %s: write to server: %w: %w", t.cfg.Name, err, core.ErrProvider)
	}
	return nil
}

func (t *stdioTransport) readLoop(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxMessageBytes)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			t.cfg.Logger.Warn("mcp: unreadable message from server", "server", t.cfg.Name, "err", err)
			continue
		}
		if resp.ID == 0 {
			continue
		}
		t.pendMu.Lock()
		ch := t.pending[resp.ID]
		t.pendMu.Unlock()
		if ch == nil {
			continue
		}
		select {
		case ch <- resp:
		default:
		}
	}
}

func (t *stdioTransport) logStderr(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 8*1024), 1<<20)
	for sc.Scan() {
		t.cfg.Logger.Debug("mcp: server stderr", "server", t.cfg.Name, "line", sc.Text())
	}
}

func (t *stdioTransport) markDone(err error) {
	t.errMu.Lock()
	if t.exitErr == nil && err != nil {
		t.exitErr = err
	}
	t.errMu.Unlock()
	t.doneOnce.Do(func() { close(t.done) })
}

func (t *stdioTransport) closedErr() error {
	t.errMu.Lock()
	defer t.errMu.Unlock()
	if t.exitErr != nil {
		return t.exitErr
	}
	return fmt.Errorf("mcp: %s: transport is closed: %w", t.cfg.Name, core.ErrProvider)
}
