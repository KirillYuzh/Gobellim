// Package sqlstore is a database/sql implementation of flow.Store. It ships a
// one-table schema and does not start a database or import a driver.
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

// Store keeps continuation nonces in one table. Exactly one replica can claim a
// nonce; the loser is refused. Expired rows are removed by Prune.
type Store struct {
	db          Execer
	table       string
	placeholder func(int) string
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

// New builds a Store over db.
func New(db Execer, opts ...Option) (*Store, error) {
	s := &Store{
		db:          db,
		table:       "gobellim_claims",
		placeholder: func(int) string { return "?" },
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

// Ensure creates the claims table if it does not exist.
func (s *Store) Ensure(ctx context.Context) error {
	query := fmt.Sprintf(
		`CREATE TABLE IF NOT EXISTS %s (nonce VARCHAR(128) PRIMARY KEY, expires_at TIMESTAMP NOT NULL)`,
		s.table,
	)
	_, err := s.db.ExecContext(ctx, query)
	return err
}

// Claim inserts the nonce. A duplicate nonce is reported as an error, which
// the workflow treats as a failed claim: fail-closed.
func (s *Store) Claim(ctx context.Context, nonce string, expiresAt time.Time) (bool, error) {
	query := fmt.Sprintf(
		"INSERT INTO %s (nonce, expires_at) VALUES (%s, %s)",
		s.table, s.placeholder(1), s.placeholder(2),
	)
	if _, err := s.db.ExecContext(ctx, query, nonce, expiresAt); err != nil {
		return false, fmt.Errorf("sqlstore: claim %s: %w", nonce, err)
	}
	return true, nil
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
