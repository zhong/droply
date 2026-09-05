package store

import (
	"fmt"

	"github.com/zhong/droply/internal/model"
)

// ---- Projects ----

func (s *SQLiteStore) CreateProject(subdomainID int64, name string) (*model.Project, error) {
	res, err := s.db.Exec(
		`INSERT INTO projects (subdomain_id, name, host_label) VALUES (?, ?, ?)`, subdomainID, name, newHostLabel("p-"),
	)
	if err != nil {
		return nil, fmt.Errorf("create project: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.getProjectByID(id)
}

func (s *SQLiteStore) getProjectByID(id int64) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE id = ?`, id,
	)
	return scanProject(row)
}

func (s *SQLiteStore) GetProject(subdomainID int64, name string) (*model.Project, error) {
	row := s.db.QueryRow(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE subdomain_id = ? AND name = ?`,
		subdomainID, name,
	)
	return scanProject(row)
}

func (s *SQLiteStore) ListProjects(subdomainID int64) ([]model.Project, error) {
	rows, err := s.db.Query(
		`SELECT id, subdomain_id, name, created_at, updated_at, host_label FROM projects WHERE subdomain_id = ? ORDER BY created_at DESC`,
		subdomainID,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var result []model.Project
	for rows.Next() {
		p, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *p)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) DeleteProject(subdomainID int64, name string) error {
	_, err := s.db.Exec(
		`DELETE FROM projects WHERE subdomain_id = ? AND name = ?`, subdomainID, name,
	)
	return err
}

type projectScanner interface{ Scan(...any) error }

func scanProject(row projectScanner) (*model.Project, error) {
	var p model.Project
	var createdAt, updatedAt string
	if err := row.Scan(&p.ID, &p.SubdomainID, &p.Name, &createdAt, &updatedAt, &p.HostLabel); err != nil {
		return nil, fmt.Errorf("scan project: %w", err)
	}
	p.CreatedAt = parseTime(createdAt)
	p.UpdatedAt = parseTime(updatedAt)
	return &p, nil
}
