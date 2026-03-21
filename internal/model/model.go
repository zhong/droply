package model

import "time"

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Password  string    `json:"-"`
	APIToken  string    `json:"api_token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type Subdomain struct {
	ID           int64     `json:"id"`
	UserID       int64     `json:"user_id"`
	Name         string    `json:"name"`
	CreatedAt    time.Time `json:"created_at"`
	ProjectCount int       `json:"project_count,omitempty"`
}

type Project struct {
	ID          int64     `json:"id"`
	SubdomainID int64     `json:"subdomain_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Deployment struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Version   int       `json:"version"`
	FileCount int       `json:"file_count"`
	TotalSize int64     `json:"total_size"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type CustomDomain struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	Domain    string    `json:"domain"`
	Verified  bool      `json:"verified"`
	CreatedAt time.Time `json:"created_at"`
}

type DomainWithPath struct {
	Domain        string `json:"domain"`
	SubdomainName string `json:"subdomain_name"`
	ProjectName   string `json:"project_name"`
}

type AccessRule struct {
	ID           int64     `json:"id"`
	SubdomainID  int64     `json:"subdomain_id"`
	ProjectID    *int64    `json:"project_id,omitempty"`
	AllowedIPs   []string  `json:"allowed_ips,omitempty"`
	PasswordHash string    `json:"-"`
	HasPassword  bool      `json:"has_password"`
	SessionTTL   int       `json:"session_ttl"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}
