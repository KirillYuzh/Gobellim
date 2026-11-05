package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gobellim "github.com/KirillYuzh/Gobellim"
	"github.com/KirillYuzh/Gobellim/flow"
)

var (
	_ flow.Store          = (*Store)(nil)
	_ gobellim.ClaimStore = (*Store)(nil)
)

type fakeExecer struct {
	queries []string
	args    [][]any
	err     error
	rows    int64
}

func (f *fakeExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.err != nil {
		return nil, f.err
	}
	return fakeResult{rows: f.rows}, nil
}

type fakeResult struct{ rows int64 }

func (r fakeResult) LastInsertId() (int64, error) { return 0, nil }
func (r fakeResult) RowsAffected() (int64, error) { return r.rows, nil }

func newFakeExecer() *fakeExecer { return &fakeExecer{rows: 1} }

func TestEnsure(t *testing.T) {
	db := newFakeExecer()
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(db.queries) != 1 || !strings.Contains(db.queries[0], "CREATE TABLE IF NOT EXISTS gobellim_claims") {
		t.Fatalf("queries = %v", db.queries)
	}
}

func TestClaim(t *testing.T) {
	db := newFakeExecer()
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.Claim(context.Background(), "nonce-1", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("first claim lost")
	}
	if !strings.Contains(db.queries[0], "INSERT INTO gobellim_claims") {
		t.Fatalf("query = %q", db.queries[0])
	}
	if !strings.Contains(db.queries[0], "ON CONFLICT DO NOTHING") {
		t.Fatalf("query has no conflict clause: %q", db.queries[0])
	}
	if len(db.args[0]) != 2 {
		t.Fatalf("args = %v", db.args[0])
	}
}

func TestClaimDuplicateIsNotAnError(t *testing.T) {
	db := newFakeExecer()
	db.rows = 0
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.Claim(context.Background(), "nonce-1", time.Now())
	if err != nil {
		t.Fatalf("a duplicate must not look like a database failure: %v", err)
	}
	if ok {
		t.Fatal("a duplicate nonce was claimed")
	}
}

func TestClaimErrorIsFailClosed(t *testing.T) {
	boom := errors.New("database is down")
	db := &fakeExecer{err: boom}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := s.Claim(context.Background(), "nonce-1", time.Now())
	if ok {
		t.Fatal("claim reported success on a database error")
	}
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
}

func TestPlaceholder(t *testing.T) {
	db := newFakeExecer()
	s, err := New(db, Placeholder(func(i int) string { return fmt.Sprintf("$%d", i) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(context.Background(), "nonce-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.queries[0], "$1") || !strings.Contains(db.queries[0], "$2") {
		t.Fatalf("query = %q", db.queries[0])
	}
}

func TestConflictClause(t *testing.T) {
	db := newFakeExecer()
	s, err := New(db, ConflictClause("ON DUPLICATE KEY UPDATE nonce = nonce"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(context.Background(), "nonce-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(db.queries[0], "ON DUPLICATE KEY UPDATE nonce = nonce") {
		t.Fatalf("query = %q", db.queries[0])
	}
}

func TestPrune(t *testing.T) {
	db := newFakeExecer()
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().Add(-time.Hour)
	if err := s.Prune(context.Background(), before); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(db.queries[0], "DELETE FROM gobellim_claims WHERE expires_at <") {
		t.Fatalf("query = %q", db.queries[0])
	}
	if len(db.args[0]) != 1 || db.args[0][0] != before {
		t.Fatalf("args = %v", db.args[0])
	}
}

func TestInvalidTable(t *testing.T) {
	if _, err := New(newFakeExecer(), Table("claims; drop table users")); err == nil {
		t.Fatal("expected an error for an invalid table name")
	}
}

func TestNilDatabase(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected an error for a nil database")
	}
}
