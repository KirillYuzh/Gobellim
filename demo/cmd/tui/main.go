// Command tui is a terminal client for the demo bot.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	defaultUser = "alice"

	reset  = "\033[0m"
	dim    = "\033[2m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

func main() {
	url := flag.String("url", envOr("KIT_BOT_URL", "http://localhost:8080"), "base URL of the bot")
	timeout := flag.Duration("timeout", 90*time.Second, "HTTP timeout")
	flag.Parse()

	t := newTUI(*url, *timeout)
	if err := t.health(); err != nil {
		t.errorf("бот недоступен на %s", t.base)
		t.hint("запустите go run ./demo/cmd/bot")
		t.hint("%v", err)
		t.flush()
		os.Exit(1)
	}
	t.banner()
	t.loginAs(defaultUser)
	t.loop()
	t.flush()
}

type tui struct {
	base  string
	http  *http.Client
	in    *bufio.Reader
	out   *bufio.Writer
	tty   bool
	color bool

	token string
	login string
	role  string
}

func newTUI(base string, timeout time.Duration) *tui {
	tty := isTerminal(os.Stdout)
	return &tui{
		base:  strings.TrimRight(base, "/"),
		http:  &http.Client{Timeout: timeout},
		in:    bufio.NewReader(os.Stdin),
		out:   bufio.NewWriter(os.Stdout),
		tty:   tty,
		color: tty && os.Getenv("NO_COLOR") == "",
	}
}

func (t *tui) loop() {
	for {
		fmt.Fprint(t.out, t.paint(cyan, "› "))
		t.flush()
		line, err := t.in.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil {
			if line != "" {
				t.handle(line)
			}
			fmt.Fprintln(t.out)
			return
		}
		if line != "" && !t.handle(line) {
			return
		}
	}
}

func (t *tui) handle(line string) bool {
	name, arg := command(line)
	switch name {
	case "/help":
		t.help()
	case "/login":
		if arg == "" {
			t.hint("использование: /login <user>")
			return true
		}
		t.loginAs(arg)
	case "/me":
		t.me()
	case "/kb":
		t.kb(arg)
	case "/clear":
		t.clear()
	case "/quit", "/exit":
		return false
	default:
		if strings.HasPrefix(name, "/") {
			t.hint("неизвестная команда %s, /help", name)
			return true
		}
		t.ask(line)
	}
	return true
}

func (t *tui) ask(prompt string) {
	var resp struct {
		Answer string `json:"answer"`
	}
	start := time.Now()
	err := t.post("/bot/ask", map[string]string{"prompt": prompt}, &resp)
	elapsed := time.Since(start)
	if err != nil {
		t.fail(err)
		return
	}
	fmt.Fprintln(t.out, resp.Answer)
	t.elapsed(elapsed)
}

func (t *tui) kb(query string) {
	if query == "" {
		t.hint("использование: /kb <запрос>")
		return
	}
	var resp struct {
		Hits []struct {
			DocID string `json:"doc_id"`
			Title string `json:"title"`
			Text  string `json:"text"`
		} `json:"hits"`
	}
	start := time.Now()
	err := t.post("/kb/search", map[string]any{"query": query, "top_k": 3}, &resp)
	elapsed := time.Since(start)
	if err != nil {
		t.fail(err)
		return
	}
	if len(resp.Hits) == 0 {
		t.hint("ничего не найдено")
		return
	}
	for _, hit := range resp.Hits {
		title := hit.Title
		if title == "" {
			title = hit.DocID
		}
		fmt.Fprintf(t.out, "%s %s\n", t.paint(bold, title), t.paint(dim, hit.DocID))
		fmt.Fprintf(t.out, "  %s\n", snippet(hit.Text, 200))
	}
	t.elapsed(elapsed)
}

func (t *tui) me() {
	var resp struct {
		UserID string `json:"user_id"`
		Login  string `json:"login"`
		Role   string `json:"role"`
		Tenant string `json:"tenant"`
	}
	if err := t.get("/auth/me", &resp); err != nil {
		t.fail(err)
		return
	}
	fmt.Fprintf(t.out, "%s %s %s %s %s\n",
		resp.Login,
		t.paint(roleColor(resp.Role), resp.Role),
		t.paint(dim, "·"),
		t.paint(dim, "tenant "+resp.Tenant),
		t.paint(dim, "· "+resp.UserID),
	)
}

func (t *tui) loginAs(user string) {
	var resp struct {
		Token string `json:"token"`
		Login string `json:"login"`
		Role  string `json:"role"`
	}
	if err := t.post("/auth/login", map[string]string{"login": user}, &resp); err != nil {
		t.fail(err)
		return
	}
	t.token, t.login, t.role = resp.Token, resp.Login, resp.Role
	t.status()
}

func (t *tui) clear() {
	if t.tty {
		fmt.Fprint(t.out, "\033[2J\033[H")
	}
	t.banner()
	if t.token != "" {
		t.status()
	}
}

func (t *tui) banner() {
	fmt.Fprintln(t.out, t.paint(bold, "kit · demo bot"))
	fmt.Fprintln(t.out, t.paint(dim, t.base))
	fmt.Fprintln(t.out, t.paint(dim, "/help — команды"))
}

func (t *tui) status() {
	fmt.Fprintf(t.out, "%s %s %s %s\n",
		t.paint(green, "✓"),
		t.login,
		t.paint(dim, "·"),
		t.paint(roleColor(t.role), t.role),
	)
}

func (t *tui) help() {
	for _, row := range [][2]string{
		{"/help", "команды"},
		{"/login <user>", "перелогиниться (alice|root)"},
		{"/me", "кто я"},
		{"/kb <запрос>", "поиск по базе знаний"},
		{"/clear", "очистить экран"},
		{"/quit, /exit", "выйти"},
		{"<текст>", "вопрос боту"},
	} {
		fmt.Fprintf(t.out, "  %s %s\n", t.paint(cyan, fmt.Sprintf("%-14s", row[0])), t.paint(dim, row[1]))
	}
}

func (t *tui) health() error {
	var resp struct {
		Status string `json:"status"`
	}
	if err := t.get("/healthz", &resp); err != nil {
		return err
	}
	if resp.Status != "ok" {
		return fmt.Errorf("healthz вернул %q", resp.Status)
	}
	return nil
}

func (t *tui) fail(err error) {
	var api *apiError
	if errors.As(err, &api) {
		switch api.status {
		case http.StatusUnauthorized:
			t.errorf("токен истёк, /login <user>")
		case http.StatusForbidden:
			t.errorf("нет прав: нужен admin")
		default:
			t.errorf("%s: %s", http.StatusText(api.status), api.message)
		}
		return
	}
	t.errorf("%v", err)
}

func (t *tui) errorf(format string, a ...any) {
	fmt.Fprintf(t.out, "%s %s\n", t.paint(red, "✗"), t.paint(red, fmt.Sprintf(format, a...)))
}

func (t *tui) hint(format string, a ...any) {
	fmt.Fprintf(t.out, "%s %s\n", t.paint(dim, "·"), t.paint(dim, fmt.Sprintf(format, a...)))
}

func (t *tui) elapsed(d time.Duration) {
	fmt.Fprintln(t.out, t.paint(dim, fmt.Sprintf("· %.1fs", d.Seconds())))
}

func (t *tui) flush() {
	_ = t.out.Flush()
}

func (t *tui) paint(code, s string) string {
	if !t.color {
		return s
	}
	return code + s + reset
}

func (t *tui) get(path string, out any) error {
	return t.do(http.MethodGet, path, nil, out)
}

func (t *tui) post(path string, body, out any) error {
	return t.do(http.MethodPost, path, body, out)
}

func (t *tui) do(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, t.base+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &apiError{status: resp.StatusCode, message: errorMessage(data)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("некорректный ответ бота: %w", err)
	}
	return nil
}

type apiError struct {
	status  int
	message string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("%s: %s", http.StatusText(e.status), e.message)
}

func errorMessage(data []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &payload) == nil && payload.Error != "" {
		return payload.Error
	}
	if msg := strings.TrimSpace(string(data)); msg != "" {
		return msg
	}
	return "без описания"
}

func roleColor(role string) string {
	if role == "admin" {
		return yellow
	}
	return cyan
}

func snippet(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func command(line string) (string, string) {
	name, arg, _ := strings.Cut(line, " ")
	return name, strings.TrimSpace(arg)
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
