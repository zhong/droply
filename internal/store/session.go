package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"time"
)

const ConsoleSessionLifetime = 8 * time.Hour

type ConsoleSession struct {
	UserID    int64
	ExpiresAt time.Time
}
type ConsoleSessionStore interface {
	CreateConsoleSession(context.Context, int64) (*ConsoleSession, string, error)
	GetConsoleSession(context.Context, string) (*ConsoleSession, error)
	RevokeConsoleSession(context.Context, string) error
}

func (s *SQLiteStore) migrateConsoleSessions() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS console_sessions (digest TEXT PRIMARY KEY,user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,expires_at TEXT NOT NULL);CREATE INDEX IF NOT EXISTS console_sessions_expiry ON console_sessions(expires_at);`)
	return err
}
func (s *SQLiteStore) CreateConsoleSession(ctx context.Context, userID int64) (*ConsoleSession, string, error) {
	now := time.Now().UTC()
	expires := now.Add(ConsoleSessionLifetime)
	raw := rand.Text() + rand.Text()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, "", err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM console_sessions WHERE expires_at <= ?`, now.Format(time.RFC3339Nano)); err != nil {
		return nil, "", err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO console_sessions(digest,user_id,expires_at)VALUES(?,?,?)`, tokenDigest(raw), userID, expires.Format(time.RFC3339Nano)); err != nil {
		return nil, "", err
	}
	if err = tx.Commit(); err != nil {
		return nil, "", err
	}
	return &ConsoleSession{UserID: userID, ExpiresAt: expires}, raw, nil
}
func (s *SQLiteStore) GetConsoleSession(ctx context.Context, raw string) (*ConsoleSession, error) {
	if len(raw) != 52 {
		return nil, sql.ErrNoRows
	}
	var session ConsoleSession
	var expires string
	if err := s.db.QueryRowContext(ctx, `SELECT user_id,expires_at FROM console_sessions WHERE digest=?`, tokenDigest(raw)).Scan(&session.UserID, &expires); err != nil {
		return nil, err
	}
	var err error
	session.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil || !time.Now().Before(session.ExpiresAt) {
		return nil, sql.ErrNoRows
	}
	return &session, nil
}
func (s *SQLiteStore) RevokeConsoleSession(ctx context.Context, raw string) error {
	if len(raw) != 52 {
		return errors.New("invalid session")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM console_sessions WHERE digest=?`, tokenDigest(raw))
	return err
}
