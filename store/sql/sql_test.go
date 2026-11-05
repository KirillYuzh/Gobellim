package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type fakeExecer struct {
	queries []string
	args    [][]any
	err     error
}

func (f *fakeExecer) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	f.queries = append(f.queries, query)
	f.args = append(f.args, args)
	if f.err != nil {
		return nil, f.err
	}
	return nil, nil
}

func TestEnsure(t *testing.T) {
	db := &fakeExecer{}
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
	db := &fakeExecer{}
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
	if len(db.args[0]) != 2 {
		t.Fatalf("args = %v", db.args[0])
	}
}

func TestClaimErrorIsFailClosed(t *testing.T) {
	boom := errors.New("duplicate")
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
	db := &fakeExecer{}
	s, err := New(db, Placeholder(func(i int) string { return fmt.Sprintf("$%d", i) }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(context.Background(), "n", time.Now()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.queries[0], "$1") || !strings.Contains(db.queries[0], "$2") {
		t.Fatalf("query = %q", db.queries[0])
	}
}

func TestPrune(t *testing.T) {
	db := &fakeExecer{}
	s, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	if err := s.Prune(context.Background(), before); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(db.queries[0], "DELETE FROM gobellim_claims") {
		t.Fatalf("query = %q", db.queries[0])
	}
	if len(db.args[0]) != 1 {
		t.Fatalf("args = %v", db.args[0])
	}
}

func TestInvalidTable(t *testing.T) {
	if _, err := New(&fakeExecer{}, Table("bad name; drop table")); err == nil {
		t.Fatal("accepted an invalid table name")
	}
}

func TestNilDatabase(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("accepted a nil database")
	}
}
