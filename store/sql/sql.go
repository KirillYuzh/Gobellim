// Package sqlstore is a database/sql implementation of flow.Store. It does not
// start a database, import a driver or manage a schema: the claims table is the
// application's migration, so the statement is part of its schema history.
//
// PostgreSQL:
//
//	CREATE TABLE gobellim_claims (
//	    nonce      TEXT PRIMARY KEY,
//	    expires_at TIMESTAMPTZ NOT NULL
//	);
//	CREATE INDEX gobellim_claims_expires_at ON gobellim_claims (expires_at);
//
// MySQL and SQLite accept the same columns; use DATETIME(6) and VARCHAR(128) on
// MySQL.
//
// On MySQL build the store with InsertPrefix("INSERT IGNORE") and
// ConflictClause(""). Do not use ConflictClause("ON DUPLICATE KEY UPDATE
// nonce = nonce"): with the clientFoundRows=true connection flag, which some
// drivers and ORMs enable, MySQL reports one affected row for a duplicate, and
// Claim would treat an already-used nonce as freshly claimed. INSERT IGNORE
// reports zero affected rows for a duplicate regardless of that flag.
//
// The index on expires_at keeps Prune from scanning the table.
package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"
)

var identRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Execer is the subset of *sql.DB the store needs. *sql.DB satisfies it.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Store keeps approval nonces in one table. Exactly one replica can claim a
// nonce; the loser gets (false, nil). Expired rows are removed by Prune.
type Store struct {
	db          Execer
	table       string
	insert      string
	placeholder func(int) string
	conflict    string
}

// Option configures a Store.
type Option func(*Store) error

// Table overrides the table name. It must be a plain SQL identifier.
func Table(name string) Option {
	return func(s *Store) error {
		if !identRE.MatchString(name) {
			return fmt.Errorf("sqlstore: invalid table name %q", name)
		}
		s.table = name
		return nil
	}
}

// Placeholder overrides the parameter placeholder. It receives a 1-based
// parameter index and returns the text to emit; "?" is the default, "$%d" fits
// PostgreSQL.
func Placeholder(fn func(int) string) Option {
	return func(s *Store) error {
		if fn != nil {
			s.placeholder = fn
		}
		return nil
	}
}

// ConflictClause overrides the clause appended to the INSERT. The default
// "ON CONFLICT DO NOTHING" fits PostgreSQL and SQLite. An empty clause makes a
// duplicate nonce a database error instead of a lost claim. On MySQL combine an
// empty clause with InsertPrefix("INSERT IGNORE").
func ConflictClause(sql string) Option {
	return func(s *Store) error {
		s.conflict = sql
		return nil
	}
}

// InsertPrefix overrides the verb the statement starts with. The default is
// "INSERT"; MySQL needs "INSERT IGNORE" so that a duplicate nonce reports zero
// affected rows even when the connection sets clientFoundRows. No other value
// is accepted: the verb is concatenated into the statement.
func InsertPrefix(verb string) Option {
	return func(s *Store) error {
		if verb != "INSERT" && verb != "INSERT IGNORE" {
			return fmt.Errorf("sqlstore: insert prefix must be INSERT or INSERT IGNORE, got %q", verb)
		}
		s.insert = verb
		return nil
	}
}

// New builds a Store over db.
func New(db Execer, opts ...Option) (*Store, error) {
	s := &Store{
		db:          db,
		table:       "gobellim_claims",
		insert:      "INSERT",
		placeholder: func(int) string { return "?" },
		conflict:    "ON CONFLICT DO NOTHING",
	}
	for _, o := range opts {
		if err := o(s); err != nil {
			return nil, err
		}
	}
	if db == nil {
		return nil, fmt.Errorf("sqlstore: database is required")
	}
	return s, nil
}

// Claim inserts the nonce and reports whether this caller won the race. A
// duplicate nonce is (false, nil); a database error is (false, err), which the
// workflow treats as a failed claim.
func (s *Store) Claim(ctx context.Context, nonce string, expiresAt time.Time) (bool, error) {
	query := fmt.Sprintf(
		"%s INTO %s (nonce, expires_at) VALUES (%s, %s)",
		s.insert, s.table, s.placeholder(1), s.placeholder(2),
	)
	if s.conflict != "" {
		query += " " + s.conflict
	}
	res, err := s.db.ExecContext(ctx, query, nonce, expiresAt)
	if err != nil {
		return false, fmt.Errorf("sqlstore: claim %s: %w", nonce, err)
	}
	if res == nil {
		return false, fmt.Errorf("sqlstore: claim %s: driver returned no result", nonce)
	}
	n, err := res.RowsAffected()
	if err != nil {
		// An unverifiable claim must not execute the action.
		return false, fmt.Errorf("sqlstore: claim %s: rows affected: %w", nonce, err)
	}
	return n > 0, nil
}

// Prune removes nonces that expired before the given time. A zero time means
// now. The application decides how often to call it.
func (s *Store) Prune(ctx context.Context, before time.Time) error {
	if before.IsZero() {
		before = time.Now().UTC()
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE expires_at < %s", s.table, s.placeholder(1))
	_, err := s.db.ExecContext(ctx, query, before)
	return err
}
