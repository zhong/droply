package store

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCreateAndGetUser(t *testing.T) {
	s := newTestStore(t)

	user, err := s.CreateUser("alice@example.com", "hashed_pw", "token-abc")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID == 0 {
		t.Error("expected non-zero user ID")
	}
	if user.Email != "alice@example.com" {
		t.Errorf("expected email alice@example.com, got %s", user.Email)
	}

	byEmail, err := s.GetUserByEmail("alice@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != user.ID {
		t.Errorf("GetUserByEmail: id mismatch: %d != %d", byEmail.ID, user.ID)
	}
	if byEmail.Password != "hashed_pw" {
		t.Errorf("expected password hashed_pw, got %s", byEmail.Password)
	}

	byToken, err := s.GetUserByToken("token-abc")
	if err != nil {
		t.Fatalf("GetUserByToken: %v", err)
	}
	if byToken.ID != user.ID {
		t.Errorf("GetUserByToken: id mismatch: %d != %d", byToken.ID, user.ID)
	}
}

func TestCreateDuplicateUser(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.CreateUser("bob@example.com", "pw1", "tok1"); err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	if _, err := s.CreateUser("bob@example.com", "pw2", "tok2"); err == nil {
		t.Error("expected error for duplicate email, got nil")
	}
}

func TestSubdomainCRUD(t *testing.T) {
	s := newTestStore(t)

	user, err := s.CreateUser("carol@example.com", "pw", "tok-carol")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sd, err := s.CreateSubdomain(user.ID, "mysite")
	if err != nil {
		t.Fatalf("CreateSubdomain: %v", err)
	}
	if sd.Name != "mysite" {
		t.Errorf("expected name mysite, got %s", sd.Name)
	}
	if sd.UserID != user.ID {
		t.Errorf("expected user_id %d, got %d", user.ID, sd.UserID)
	}

	list, err := s.ListSubdomains(user.ID)
	if err != nil {
		t.Fatalf("ListSubdomains: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 subdomain, got %d", len(list))
	}

	byName, err := s.GetSubdomainByName("mysite")
	if err != nil {
		t.Fatalf("GetSubdomainByName: %v", err)
	}
	if byName.ID != sd.ID {
		t.Errorf("GetSubdomainByName id mismatch: %d != %d", byName.ID, sd.ID)
	}

	if err := s.DeleteSubdomain(user.ID, "mysite"); err != nil {
		t.Fatalf("DeleteSubdomain: %v", err)
	}
	list, err = s.ListSubdomains(user.ID)
	if err != nil {
		t.Fatalf("ListSubdomains after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 subdomains after delete, got %d", len(list))
	}
}

func TestProjectCRUD(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("dave@example.com", "pw", "tok-dave")
	sd, _ := s.CreateSubdomain(user.ID, "davesub")

	proj, err := s.CreateProject(sd.ID, "homepage")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if proj.Name != "homepage" {
		t.Errorf("expected name homepage, got %s", proj.Name)
	}
	if proj.SubdomainID != sd.ID {
		t.Errorf("expected subdomain_id %d, got %d", sd.ID, proj.SubdomainID)
	}

	got, err := s.GetProject(sd.ID, "homepage")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != proj.ID {
		t.Errorf("GetProject id mismatch: %d != %d", got.ID, proj.ID)
	}

	if _, err := s.CreateProject(sd.ID, "blog"); err != nil {
		t.Fatalf("CreateProject blog: %v", err)
	}

	list, err := s.ListProjects(sd.ID)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(list))
	}

	if err := s.DeleteProject(sd.ID, "homepage"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	list, err = s.ListProjects(sd.ID)
	if err != nil {
		t.Fatalf("ListProjects after delete: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 project after delete, got %d", len(list))
	}
}

func TestDeploymentLifecycle(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("eve@example.com", "pw", "tok-eve")
	sd, _ := s.CreateSubdomain(user.ID, "evesub")
	proj, _ := s.CreateProject(sd.ID, "app")

	d1, err := s.CreateDeployment(proj.ID, 5, 1024)
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if d1.Status != "uploading" {
		t.Errorf("expected status uploading, got %s", d1.Status)
	}
	if d1.Version != 1 {
		t.Errorf("expected version 1, got %d", d1.Version)
	}

	if err := s.ActivateDeployment(d1.ID); err != nil {
		t.Fatalf("ActivateDeployment: %v", err)
	}

	deployments, err := s.ListDeployments(proj.ID)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 1 {
		t.Fatalf("expected 1 deployment, got %d", len(deployments))
	}
	if deployments[0].Status != "active" {
		t.Errorf("expected status active, got %s", deployments[0].Status)
	}

	// Second deployment should auto-increment version and archive previous active
	d2, err := s.CreateDeployment(proj.ID, 6, 2048)
	if err != nil {
		t.Fatalf("CreateDeployment v2: %v", err)
	}
	if d2.Version != 2 {
		t.Errorf("expected version 2, got %d", d2.Version)
	}
	if err := s.ActivateDeployment(d2.ID); err != nil {
		t.Fatalf("ActivateDeployment d2: %v", err)
	}

	deployments, err = s.ListDeployments(proj.ID)
	if err != nil {
		t.Fatalf("ListDeployments after v2: %v", err)
	}
	if len(deployments) != 2 {
		t.Fatalf("expected 2 deployments, got %d", len(deployments))
	}
	// Ordered by version DESC: first is v2 (active), second is v1 (archived)
	if deployments[0].Status != "active" {
		t.Errorf("expected d2 active, got %s", deployments[0].Status)
	}
	if deployments[1].Status != "archived" {
		t.Errorf("expected d1 archived, got %s", deployments[1].Status)
	}
}

func TestCustomDomainCRUD(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("frank@example.com", "pw", "tok-frank")
	sd, _ := s.CreateSubdomain(user.ID, "franksub")
	proj, _ := s.CreateProject(sd.ID, "site")

	cd, err := s.CreateCustomDomain(proj.ID, "example.com")
	if err != nil {
		t.Fatalf("CreateCustomDomain: %v", err)
	}
	if cd.Verified {
		t.Error("expected verified=false after creation")
	}
	if cd.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", cd.Domain)
	}

	if err := s.VerifyCustomDomain("example.com"); err != nil {
		t.Fatalf("VerifyCustomDomain: %v", err)
	}
	got, err := s.GetCustomDomain("example.com")
	if err != nil {
		t.Fatalf("GetCustomDomain: %v", err)
	}
	if !got.Verified {
		t.Error("expected verified=true after VerifyCustomDomain")
	}

	list, err := s.ListCustomDomains(proj.ID)
	if err != nil {
		t.Fatalf("ListCustomDomains: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 custom domain, got %d", len(list))
	}

	// ListAllVerifiedDomainsWithPaths should include this domain
	paths, err := s.ListAllVerifiedDomainsWithPaths()
	if err != nil {
		t.Fatalf("ListAllVerifiedDomainsWithPaths: %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("expected 1 verified domain path, got %d", len(paths))
	}
	if paths[0].Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", paths[0].Domain)
	}
	if paths[0].SubdomainName != "franksub" {
		t.Errorf("expected subdomain franksub, got %s", paths[0].SubdomainName)
	}
	if paths[0].ProjectName != "site" {
		t.Errorf("expected project site, got %s", paths[0].ProjectName)
	}

	if err := s.DeleteCustomDomain(proj.ID, "example.com"); err != nil {
		t.Fatalf("DeleteCustomDomain: %v", err)
	}
	list, err = s.ListCustomDomains(proj.ID)
	if err != nil {
		t.Fatalf("ListCustomDomains after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 custom domains after delete, got %d", len(list))
	}
}

func TestDomainVerificationMigrationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "droply.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateUser("migration@example.com", "hash", "migration-token")
	if err != nil {
		t.Fatal(err)
	}
	sub, err := s.CreateSubdomain(user.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	project, err := s.CreateProject(sub.ID, "blog")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomDomain(project.ID, "legacy.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyCustomDomain("legacy.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateCustomDomain(project.ID, "pending.example.com"); err != nil {
		t.Fatal(err)
	}
	// Recreate the old schema shape before opening it with the new binary.
	if _, err := s.db.Exec(`ALTER TABLE custom_domains DROP COLUMN verification_token`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := s.GetCustomDomain("legacy.example.com")
	if err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetCustomDomain("pending.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Verified || pending.Verified || pending.VerificationToken == "" {
		t.Fatalf("migration lost state: %+v %+v", verified, pending)
	}
	token := pending.VerificationToken
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, err = s.GetCustomDomain("pending.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if pending.VerificationToken != token {
		t.Fatal("challenge changed on restart")
	}
	if err := s.VerifyCustomDomainChallenge(pending.Domain, token); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCustomDomain(project.ID, pending.Domain); err != nil {
		t.Fatal(err)
	}
	rebound, err := s.CreateCustomDomain(project.ID, pending.Domain)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyCustomDomainChallenge(rebound.Domain, token); err == nil {
		t.Fatal("stale challenge verified a new binding")
	}
	if err := s.DeleteProject(sub.ID, project.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetCustomDomain(verified.Domain); err == nil {
		t.Fatal("project deletion did not cascade")
	}
}
