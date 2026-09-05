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
	HostLabel   string    `json:"host_label"`
	ID          int64     `json:"id"`
	SubdomainID int64     `json:"subdomain_id"`
	Name        string    `json:"name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Deployment struct {
	Environment   string    `json:"environment"`
	Branch        string    `json:"branch,omitempty"`
	Commit        string    `json:"commit,omitempty"`
	PreviewLabel  string    `json:"preview_label,omitempty"`
	BranchLabel   string    `json:"branch_label,omitempty"`
	ArtifactID    string    `json:"artifact_id,omitempty"`
	ArtifactState string    `json:"artifact_state"`
	Available     bool      `json:"available"`
	FailureReason string    `json:"failure_reason,omitempty"`
	Checksum      string    `json:"checksum,omitempty"`
	Production    bool      `json:"production"`
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	Version       int       `json:"version"`
	FileCount     int       `json:"file_count"`
	TotalSize     int64     `json:"total_size"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

type CustomDomain struct {
	Status             string    `json:"status"`
	VerificationToken  string    `json:"verification_token,omitempty"`
	VerificationRecord string    `json:"verification_record,omitempty"`
	ID                 int64     `json:"id"`
	ProjectID          int64     `json:"project_id"`
	Domain             string    `json:"domain"`
	Verified           bool      `json:"verified"`
	CreatedAt          time.Time `json:"created_at"`
}

type DomainWithPath struct {
	Domain        string `json:"domain"`
	SubdomainName string `json:"subdomain_name"`
	ProjectName   string `json:"project_name"`
}

type AccessRule struct {
	ID                 int64     `json:"id"`
	SubdomainID        int64     `json:"subdomain_id"`
	ProjectID          *int64    `json:"project_id,omitempty"`
	AllowedIPs         []string  `json:"allowed_ips,omitempty"`
	PasswordHash       string    `json:"-"`
	HasPassword        bool      `json:"has_password"`
	WeWorkEnabled      bool      `json:"wework_enabled"`
	AllowedWeWorkUsers []string  `json:"allowed_wework_users,omitempty"`
	SessionTTL         int       `json:"session_ttl"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type VisitLog struct {
	Path      string `json:"path"`
	IP        string `json:"ip"`
	Referer   string `json:"referer"`
	UserAgent string `json:"user_agent"`
	VisitedAt string `json:"visited_at"`
}

type PageDailyStat struct {
	Path string `json:"path"`
	PV   int    `json:"pv"`
	UV   int    `json:"uv"`
}

// SiteTarget maps a generated hostname to a project or immutable deployment.
// Production resolves its current deployment at request time (DeploymentID zero).
type SiteTarget struct {
	SubdomainName string `json:"subdomain_name"`
	ProjectName   string `json:"project_name"`
	ProjectID     int64  `json:"project_id"`
	DeploymentID  int64  `json:"deployment_id"`
	Kind          string `json:"kind"`
}

type PublicationEvent struct {
	ID            int64     `json:"id"`
	ProjectID     int64     `json:"project_id"`
	DeploymentID  int64     `json:"deployment_id"`
	ActorID       int64     `json:"actor_id"`
	SourceVersion int       `json:"source_version"`
	Action        string    `json:"action"`
	CreatedAt     time.Time `json:"created_at"`
}
