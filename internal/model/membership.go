package model

type ProjectMember struct {
	UserID int64  `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
}

type AuthorizedProject struct {
	ProjectID     int64  `json:"project_id"`
	SubdomainName string `json:"subdomain_name"`
	Project       string `json:"project"`
	Role          string `json:"role"`
	HostLabel     string `json:"host_label"`
}
