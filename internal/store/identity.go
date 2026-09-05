package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/zhong/droply/internal/model"
)

var ErrInvitation = errors.New("invitation unavailable or invalid")
var ErrAdministratorExists = errors.New("an administrator already exists")
var ErrIdentityInput = errors.New("invalid identity input")

type IdentityStore interface {
	GetUserByID(context.Context, int64) (*model.User, error)
	BootstrapAdmin(context.Context, string, string, string) (*model.User, error)
	ClaimAdmin(context.Context, string) (*model.User, error)
	RegisterInvited(context.Context, string, string, string, string) (*model.User, error)
	CreateInvitation(context.Context, int64, string, time.Time) (*model.Invitation, string, error)
	ListInvitations(context.Context) ([]model.Invitation, error)
	RevokeInvitation(context.Context, int64) error
}

func (s *SQLiteStore) migrateIdentity() error {
	columns, err := s.tableColumns("users")
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !columns["is_admin"] {
		if _, err := tx.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`CREATE TABLE IF NOT EXISTS invitations(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 email TEXT NOT NULL,
 digest TEXT NOT NULL UNIQUE,
 created_at TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 revoked INTEGER NOT NULL DEFAULT 0,
 used INTEGER NOT NULL DEFAULT 0,
 created_by INTEGER NOT NULL REFERENCES users(id)
 )`)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) GetUserByID(ctx context.Context, id int64) (*model.User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT id,email,password,api_token,created_at,is_admin FROM users WHERE id=?`, id))
}

// BootstrapAdmin is exclusively a local operator operation. Existing identities
// are never silently promoted; ClaimAdmin explicitly names one during upgrades.
func (s *SQLiteStore) BootstrapAdmin(ctx context.Context, email, hash, token string) (*model.User, error) {
	email = strings.TrimSpace(email)
	if email == "" || hash == "" || token == "" {
		return nil, ErrIdentityInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_admin=1`).Scan(&count); err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, ErrAdministratorExists
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(email,password,api_token,is_admin) VALUES(?,?,?,1)`, email, hash, token)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}
func (s *SQLiteStore) ClaimAdmin(ctx context.Context, email string) (*model.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE is_admin=1`).Scan(&count); err != nil {
		return nil, err
	}
	if count != 0 {
		return nil, ErrAdministratorExists
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email=?`, strings.TrimSpace(email)).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET is_admin=1 WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}
func (s *SQLiteStore) CreateInvitation(ctx context.Context, adminID int64, email string, expires time.Time) (*model.Invitation, string, error) {
	now := time.Now().UTC()
	email = strings.TrimSpace(email)
	if expires.IsZero() {
		expires = now.Add(24 * time.Hour)
	}
	if email == "" || len(email) > 320 || !expires.After(now) || expires.After(now.Add(30*24*time.Hour)) {
		return nil, "", ErrIdentityInput
	}
	raw := "dply_i_" + rand.Text() + rand.Text()
	res, err := s.db.ExecContext(ctx, `INSERT INTO invitations(email,digest,created_at,expires_at,created_by) SELECT ?,?,?,?,id FROM users WHERE id=? AND is_admin=1`, email, tokenDigest(raw), now.Format(time.RFC3339Nano), expires.UTC().Format(time.RFC3339Nano), adminID)
	if err != nil {
		return nil, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, "", err
	}
	if n != 1 {
		return nil, "", sql.ErrNoRows
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, "", err
	}
	return &model.Invitation{ID: id, Email: email, CreatedAt: now, ExpiresAt: expires.UTC()}, raw, nil
}
func (s *SQLiteStore) ListInvitations(ctx context.Context) ([]model.Invitation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,email,created_at,expires_at,revoked,used FROM invitations ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []model.Invitation{}
	for rows.Next() {
		var i model.Invitation
		var created, expires string
		if err := rows.Scan(&i.ID, &i.Email, &created, &expires, &i.Revoked, &i.Used); err != nil {
			return nil, err
		}
		i.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		i.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
		if err != nil {
			return nil, err
		}
		result = append(result, i)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) RevokeInvitation(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE invitations SET revoked=1 WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return sql.ErrNoRows
	}
	return nil
}
func (s *SQLiteStore) RegisterInvited(ctx context.Context, email, hash, token, invite string) (*model.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	var expires string
	err = tx.QueryRowContext(ctx, `SELECT id,expires_at FROM invitations WHERE digest=? AND email=? AND used=0 AND revoked=0`, tokenDigest(invite), strings.TrimSpace(email)).Scan(&id, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrInvitation
	}
	if err != nil {
		return nil, err
	}
	deadline, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(deadline) {
		return nil, ErrInvitation
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO users(email,password,api_token) VALUES(?,?,?)`, strings.TrimSpace(email), hash, token)
	if err != nil {
		return nil, err
	}
	userID, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invitations SET used=1 WHERE id=?`, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, userID)
}
