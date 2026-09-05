package model

import "time"

// AuditEvent contains identifiers and outcomes only, never credentials or request bodies.
type AuditEvent struct {
	ID          int64     `json:"id"`
	ProjectID   int64     `json:"project_id"`
	SubdomainID int64     `json:"subdomain_id"`
	ActorKind   string    `json:"actor_kind"`
	ActorID     int64     `json:"actor_id"`
	UserID      int64     `json:"user_id"`
	Action      string    `json:"action"`
	Target      string    `json:"target"`
	Result      string    `json:"result"`
	StatusCode  int       `json:"status_code"`
	CreatedAt   time.Time `json:"created_at"`
}
