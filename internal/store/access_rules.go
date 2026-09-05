package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/zhong/droply/internal/model"
)

// AccessRuleInput describes the complete persisted rule; a nil ProjectID targets the subdomain.
type AccessRuleInput struct {
	SubdomainID        int64
	ProjectID          *int64
	AllowedIPs         []string
	PasswordHash       string
	SessionTTL         int
	WeWorkEnabled      bool
	AllowedWeWorkUsers []string
}

func (s *SQLiteStore) PutAccessRule(ctx context.Context, input AccessRuleInput) (*model.AccessRule, error) {
	var ipsJSON any
	if len(input.AllowedIPs) > 0 {
		b, err := json.Marshal(input.AllowedIPs)
		if err != nil {
			return nil, fmt.Errorf("marshal allowed_ips: %w", err)
		}
		ipsJSON = string(b)
	}

	var pwHash any
	if input.PasswordHash != "" {
		pwHash = input.PasswordHash
	}

	var weWorkUsersJSON any
	if len(input.AllowedWeWorkUsers) > 0 {
		b, err := json.Marshal(input.AllowedWeWorkUsers)
		if err != nil {
			return nil, fmt.Errorf("marshal allowed_wework_users: %w", err)
		}
		weWorkUsersJSON = string(b)
	}

	weWorkInt := 0
	if input.WeWorkEnabled {
		weWorkInt = 1
	}

	// Check if rule already exists (needed because UNIQUE doesn't match NULL=NULL)
	existing, err := s.GetAccessRule(ctx, input.SubdomainID, input.ProjectID)
	if err != nil {
		return nil, fmt.Errorf("check existing access rule: %w", err)
	}

	if existing != nil {
		// Update existing rule
		_, err = s.db.ExecContext(ctx,
			`UPDATE access_rules SET allowed_ips = ?, password_hash = ?, session_ttl = ?,
				wework_enabled = ?, allowed_wework_users = ?,
				updated_at = strftime('%Y-%m-%d %H:%M:%S', 'now')
			 WHERE id = ?`,
			ipsJSON, pwHash, input.SessionTTL, weWorkInt, weWorkUsersJSON, existing.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("update access rule: %w", err)
		}
	} else {
		// Insert new rule
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO access_rules (subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			input.SubdomainID, input.ProjectID, ipsJSON, pwHash, input.SessionTTL, weWorkInt, weWorkUsersJSON,
		)
		if err != nil {
			return nil, fmt.Errorf("create access rule: %w", err)
		}
	}

	return s.GetAccessRule(ctx, input.SubdomainID, input.ProjectID)
}

func (s *SQLiteStore) GetAccessRule(ctx context.Context, subdomainID int64, projectID *int64) (*model.AccessRule, error) {
	var row *sql.Row
	if projectID == nil {
		row = s.db.QueryRowContext(ctx,
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID,
		)
	} else {
		row = s.db.QueryRowContext(ctx,
			`SELECT id, subdomain_id, project_id, allowed_ips, password_hash, session_ttl, wework_enabled, allowed_wework_users, created_at, updated_at
			 FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID,
		)
	}
	rule, err := scanAccessRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get access rule: %w", err)
	}
	return rule, nil
}

func (s *SQLiteStore) DeleteAccessRule(ctx context.Context, subdomainID int64, projectID *int64) error {
	if projectID == nil {
		_, err := s.db.ExecContext(ctx,
			`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id IS NULL`, subdomainID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM access_rules WHERE subdomain_id = ? AND project_id = ?`, subdomainID, *projectID,
	)
	return err
}

func (s *SQLiteStore) FindAccessRuleForSite(ctx context.Context, subdomainName string, projectName string) (*model.AccessRule, error) {
	// Try project-level first
	if projectName != "" {
		row := s.db.QueryRowContext(ctx,
			`SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.wework_enabled, ar.allowed_wework_users, ar.created_at, ar.updated_at
			 FROM access_rules ar
			 JOIN subdomains s ON ar.subdomain_id = s.id
			 JOIN projects p ON ar.project_id = p.id AND p.subdomain_id = s.id
			 WHERE s.name = ? AND p.name = ?`,
			subdomainName, projectName,
		)
		rule, err := scanAccessRule(row)
		if err == nil {
			return rule, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("find project access rule: %w", err)
		}
	}

	// Fall back to subdomain-level
	row := s.db.QueryRowContext(ctx,
		`SELECT ar.id, ar.subdomain_id, ar.project_id, ar.allowed_ips, ar.password_hash, ar.session_ttl, ar.wework_enabled, ar.allowed_wework_users, ar.created_at, ar.updated_at
		 FROM access_rules ar
		 JOIN subdomains s ON ar.subdomain_id = s.id
		 WHERE s.name = ? AND ar.project_id IS NULL`,
		subdomainName,
	)
	rule, err := scanAccessRule(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find subdomain access rule: %w", err)
	}
	return rule, nil
}

func (s *SQLiteStore) HasAccessRules(ctx context.Context, subdomainID int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM access_rules WHERE subdomain_id = ?`, subdomainID,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("has access rules: %w", err)
	}
	return count > 0, nil
}

func scanAccessRule(row *sql.Row) (*model.AccessRule, error) {
	var r model.AccessRule
	var projectID sql.NullInt64
	var allowedIPs sql.NullString
	var passwordHash sql.NullString
	var weWorkEnabled int
	var allowedWeWorkUsers sql.NullString
	var createdAt, updatedAt string

	if err := row.Scan(&r.ID, &r.SubdomainID, &projectID, &allowedIPs, &passwordHash, &r.SessionTTL, &weWorkEnabled, &allowedWeWorkUsers, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	if projectID.Valid {
		r.ProjectID = &projectID.Int64
	}
	if allowedIPs.Valid {
		if err := json.Unmarshal([]byte(allowedIPs.String), &r.AllowedIPs); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_ips: %w", err)
		}
	}
	if passwordHash.Valid {
		r.PasswordHash = passwordHash.String
		r.HasPassword = true
	}
	r.WeWorkEnabled = weWorkEnabled != 0
	if allowedWeWorkUsers.Valid && allowedWeWorkUsers.String != "" {
		if err := json.Unmarshal([]byte(allowedWeWorkUsers.String), &r.AllowedWeWorkUsers); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_wework_users: %w", err)
		}
	}
	r.CreatedAt = parseTimeFlexible(createdAt)
	r.UpdatedAt = parseTimeFlexible(updatedAt)
	return &r, nil
}
