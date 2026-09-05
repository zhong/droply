package store

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/zhong/droply/internal/model"
	"modernc.org/sqlite"
)

// ---- Custom Domains ----

func (s *SQLiteStore) CreateCustomDomain(projectID int64, domain string) (*model.CustomDomain, error) {
	res, err := s.db.Exec(
		`INSERT INTO custom_domains (project_id, domain, verification_token) VALUES (?, ?, ?)`, projectID, strings.ToLower(strings.TrimSuffix(domain, ".")), rand.Text(),
	)
	if err != nil {
		if sqliteErr, ok := errors.AsType[*sqlite.Error](err); ok && sqliteErr.Code() == 2067 {
			return nil, ErrDomainTaken
		}
		return nil, fmt.Errorf("create custom domain: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getCustomDomainByID(id)
}

func (s *SQLiteStore) GetCustomDomain(domain string) (*model.CustomDomain, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE domain = ?`, domain,
	)
	return scanCustomDomain(row)
}

func (s *SQLiteStore) VerifyCustomDomain(domain string) error {
	res, err := s.db.Exec(
		`UPDATE custom_domains SET verified = 1 WHERE domain = ?`, domain,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) ListCustomDomains(projectID int64) ([]model.CustomDomain, error) {
	rows, err := s.db.Query(
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE project_id = ? ORDER BY created_at DESC`,
		projectID,
	)
	if err != nil {
		return nil, fmt.Errorf("list custom domains: %w", err)
	}
	defer rows.Close()

	var result []model.CustomDomain
	for rows.Next() {
		var cd model.CustomDomain
		var createdAt string
		var verified int
		if err := rows.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt, &cd.VerificationToken); err != nil {
			return nil, fmt.Errorf("scan custom domain: %w", err)
		}
		cd.Verified = verified == 1
		cd.Status = "pending"
		if cd.Verified {
			cd.Status = "verified"
		}
		cd.VerificationRecord = "_droply-verification." + cd.Domain
		cd.CreatedAt = parseTime(createdAt)
		result = append(result, cd)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteCustomDomain(projectID int64, domain string) error {
	_, err := s.db.Exec(
		`DELETE FROM custom_domains WHERE project_id = ? AND domain = ?`, projectID, domain,
	)
	return err
}

func (s *SQLiteStore) ListAllVerifiedDomainsWithPaths() ([]model.DomainWithPath, error) {
	rows, err := s.db.Query(
		`SELECT cd.domain, s.name AS subdomain_name, p.name AS project_name
		 FROM custom_domains cd
		 JOIN projects p ON cd.project_id = p.id
		 JOIN subdomains s ON p.subdomain_id = s.id
		 WHERE cd.verified = 1
		 ORDER BY cd.domain`,
	)
	if err != nil {
		return nil, fmt.Errorf("list verified domains: %w", err)
	}
	defer rows.Close()

	var result []model.DomainWithPath
	for rows.Next() {
		var d model.DomainWithPath
		if err := rows.Scan(&d.Domain, &d.SubdomainName, &d.ProjectName); err != nil {
			return nil, fmt.Errorf("scan domain with path: %w", err)
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) getCustomDomainByID(id int64) (*model.CustomDomain, error) {
	row := s.db.QueryRow(
		`SELECT id, project_id, domain, verified, created_at, verification_token FROM custom_domains WHERE id = ?`, id,
	)
	return scanCustomDomain(row)
}

func scanCustomDomain(row *sql.Row) (*model.CustomDomain, error) {
	var cd model.CustomDomain
	var createdAt string
	var verified int
	if err := row.Scan(&cd.ID, &cd.ProjectID, &cd.Domain, &verified, &createdAt, &cd.VerificationToken); err != nil {
		return nil, fmt.Errorf("scan custom domain: %w", err)
	}
	cd.Verified = verified == 1
	cd.Status = "pending"
	if cd.Verified {
		cd.Status = "verified"
	}
	cd.VerificationRecord = "_droply-verification." + cd.Domain
	cd.CreatedAt = parseTime(createdAt)
	return &cd, nil
}
