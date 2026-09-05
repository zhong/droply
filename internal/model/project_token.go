package model

import "time"

// ProjectToken is public credential metadata. The credential digest is never serialized.
type ProjectToken struct {
	ID        int64      `json:"id"`
	ProjectID int64      `json:"project_id"`
	OwnerID   int64      `json:"owner_id"`
	IssuerID  int64      `json:"issuer_id"`
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt time.Time  `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}
