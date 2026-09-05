package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMembershipTokensStayBoundToCurrentIssuerRole(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	projectID := deploymentProject(t, s)
	members, err := s.ListProjectMembers(ctx, projectID)
	if err != nil || len(members) != 1 || members[0].Role != "owner" {
		t.Fatalf("legacy ownership %+v %v", members, err)
	}
	owner := members[0]
	issuer, err := s.CreateUser("issuer@test", "hash", "issuer")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateUser("second@test", "hash", "second")
	if err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{issuer.Email, second.Email} {
		if _, err := s.PutProjectMember(ctx, projectID, email, "deployer"); err != nil {
			t.Fatal(err)
		}
	}
	token, raw, err := s.CreateProjectToken(ctx, projectID, issuer.ID, "ci", []string{"production"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if token.IssuerID != issuer.ID || token.OwnerID != owner.UserID {
		t.Fatalf("wrong issuer %+v", token)
	}
	if err := s.RevokeProjectToken(ctx, projectID, second.ID, token.ID); err == nil {
		t.Fatal("other deployer revoked credential")
	}
	listed, err := s.ListProjectTokens(ctx, projectID, second.ID)
	if err != nil || len(listed) != 0 {
		t.Fatal("another issuer credential disclosed")
	}
	listed, err = s.ListProjectTokens(ctx, projectID, owner.UserID)
	if err != nil || len(listed) != 1 {
		t.Fatal("owner cannot inspect credentials")
	}
	if _, err := s.AuthenticateProjectToken(ctx, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutProjectMember(ctx, projectID, issuer.Email, "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateProjectToken(ctx, raw); err == nil {
		t.Fatal("downgraded issuer token still deploys")
	}
	if _, _, err := s.CreateProjectToken(ctx, projectID, issuer.ID, "no", nil, time.Time{}); err == nil {
		t.Fatal("viewer created token")
	}
	if _, err := s.PutProjectMember(ctx, projectID, owner.Email, "viewer"); !errors.Is(err, ErrMembership) {
		t.Fatal("owner can downgrade")
	}
	if err := s.RemoveProjectMember(ctx, projectID, owner.UserID); !errors.Is(err, ErrMembership) {
		t.Fatal("owner can be removed")
	}
	if _, err := s.PutProjectMember(ctx, projectID, issuer.Email, "deployer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_member_token_revoke BEFORE UPDATE ON project_tokens BEGIN SELECT RAISE(ABORT,"fail"); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProjectMember(ctx, projectID, issuer.ID); err == nil {
		t.Fatal("injected credential revocation failure accepted")
	}
	if role, err := s.ProjectRole(ctx, projectID, issuer.ID); err != nil || role != "deployer" {
		t.Fatal("membership removal was not rolled back")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_member_token_revoke`); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveProjectMember(ctx, projectID, issuer.ID); err != nil {
		t.Fatal(err)
	}
	role, err := s.ProjectRole(ctx, projectID, issuer.ID)
	if err != nil || role != "" {
		t.Fatal("removed member authorized")
	}
	if _, err := s.PutProjectMember(ctx, projectID, issuer.Email, "deployer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateProjectToken(ctx, raw); err == nil {
		t.Fatal("readding member revived removed credentials")
	}

}

func TestM2TokenIssuerMigrationAndMembershipRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "migration.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id := deploymentProject(t, s)
	members, err := s.ListProjectMembers(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	owner := members[0]
	_, raw, err := s.CreateProjectToken(t.Context(), id, owner.UserID, "legacy", nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the M2 token shape without issuer_id; migration must backfill the original owner.
	if _, err := s.db.Exec(`ALTER TABLE project_tokens DROP COLUMN issuer_id`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	token, err := s.AuthenticateProjectToken(t.Context(), raw)
	if err != nil || token.IssuerID != owner.UserID {
		t.Fatalf("legacy token %+v %v", token, err)
	}
	projects, err := s.ListAccessibleProjects(t.Context(), owner.UserID)
	if err != nil || len(projects) != 1 || projects[0].Role != "owner" {
		t.Fatalf("legacy membership %+v %v", projects, err)
	}
}
