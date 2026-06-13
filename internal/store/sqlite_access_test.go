package store

import (
	"testing"
)

func TestCreateAndGetAccessRule(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("access@example.com", "hash", "token-access")
	sub, _ := s.CreateSubdomain(user.ID, "protected")

	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.1", "192.168.1.0/24"}, "bcrypt-hash", 3600, false, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}
	if rule.ID == 0 {
		t.Error("expected non-zero rule ID")
	}
	if rule.SubdomainID != sub.ID {
		t.Errorf("expected subdomain_id %d, got %d", sub.ID, rule.SubdomainID)
	}
	if rule.ProjectID != nil {
		t.Errorf("expected nil project_id, got %v", rule.ProjectID)
	}
	if len(rule.AllowedIPs) != 2 {
		t.Fatalf("expected 2 allowed IPs, got %d", len(rule.AllowedIPs))
	}
	if rule.AllowedIPs[0] != "10.0.0.1" {
		t.Errorf("expected first IP 10.0.0.1, got %s", rule.AllowedIPs[0])
	}
	if rule.AllowedIPs[1] != "192.168.1.0/24" {
		t.Errorf("expected second IP 192.168.1.0/24, got %s", rule.AllowedIPs[1])
	}
	if !rule.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if rule.PasswordHash != "bcrypt-hash" {
		t.Errorf("expected password hash bcrypt-hash, got %s", rule.PasswordHash)
	}
	if rule.SessionTTL != 3600 {
		t.Errorf("expected session_ttl 3600, got %d", rule.SessionTTL)
	}
	if rule.CreatedAt.IsZero() {
		t.Error("expected non-zero created_at")
	}
	if rule.UpdatedAt.IsZero() {
		t.Error("expected non-zero updated_at")
	}

	// Verify GetAccessRule returns the same data
	got, err := s.GetAccessRule(sub.ID, nil)
	if err != nil {
		t.Fatalf("GetAccessRule: %v", err)
	}
	if got.ID != rule.ID {
		t.Errorf("GetAccessRule id mismatch: %d != %d", got.ID, rule.ID)
	}
	if got.PasswordHash != "bcrypt-hash" {
		t.Errorf("expected password hash bcrypt-hash, got %s", got.PasswordHash)
	}
}

func TestCreateOrUpdateAccessRuleUpsert(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("upsert@example.com", "hash", "token-upsert")
	sub, _ := s.CreateSubdomain(user.ID, "upsertsite")

	rule1, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.1"}, "pass1", 3600, false, nil)
	if err != nil {
		t.Fatalf("first CreateOrUpdateAccessRule: %v", err)
	}

	// Update the same rule (same subdomain_id, nil project_id)
	rule2, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.2", "10.0.0.3"}, "pass2", 7200, false, nil)
	if err != nil {
		t.Fatalf("second CreateOrUpdateAccessRule: %v", err)
	}

	if rule2.ID != rule1.ID {
		t.Errorf("expected same ID after upsert: %d != %d", rule2.ID, rule1.ID)
	}
	if len(rule2.AllowedIPs) != 2 {
		t.Fatalf("expected 2 allowed IPs after update, got %d", len(rule2.AllowedIPs))
	}
	if rule2.PasswordHash != "pass2" {
		t.Errorf("expected updated password hash pass2, got %s", rule2.PasswordHash)
	}
	if rule2.SessionTTL != 7200 {
		t.Errorf("expected updated session_ttl 7200, got %d", rule2.SessionTTL)
	}
}

func TestProjectLevelAccessRule(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("proj@example.com", "hash", "token-proj")
	sub, _ := s.CreateSubdomain(user.ID, "projsite")
	proj, _ := s.CreateProject(sub.ID, "app")

	projID := proj.ID
	rule, err := s.CreateOrUpdateAccessRule(sub.ID, &projID, []string{"10.0.0.1"}, "projpass", 1800, false, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule project-level: %v", err)
	}
	if rule.ProjectID == nil {
		t.Fatal("expected non-nil project_id")
	}
	if *rule.ProjectID != proj.ID {
		t.Errorf("expected project_id %d, got %d", proj.ID, *rule.ProjectID)
	}

	got, err := s.GetAccessRule(sub.ID, &projID)
	if err != nil {
		t.Fatalf("GetAccessRule project-level: %v", err)
	}
	if got.ID != rule.ID {
		t.Errorf("GetAccessRule id mismatch: %d != %d", got.ID, rule.ID)
	}
}

func TestDeleteAccessRule(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("del@example.com", "hash", "token-del")
	sub, _ := s.CreateSubdomain(user.ID, "delsite")

	_, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "pass", 3600, false, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}

	if err := s.DeleteAccessRule(sub.ID, nil); err != nil {
		t.Fatalf("DeleteAccessRule: %v", err)
	}

	got, err := s.GetAccessRule(sub.ID, nil)
	if err != nil {
		t.Fatalf("GetAccessRule after delete: %v", err)
	}
	if got != nil {
		t.Error("expected nil after delete")
	}
}

func TestFindAccessRuleForSite(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("find@example.com", "hash", "token-find")
	sub, _ := s.CreateSubdomain(user.ID, "findsite")
	proj, _ := s.CreateProject(sub.ID, "myapp")

	// Create subdomain-level rule
	_, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.1"}, "subpass", 3600, false, nil)
	if err != nil {
		t.Fatalf("create subdomain rule: %v", err)
	}

	// Create project-level rule
	projID := proj.ID
	_, err = s.CreateOrUpdateAccessRule(sub.ID, &projID, []string{"10.0.0.2"}, "projpass", 1800, false, nil)
	if err != nil {
		t.Fatalf("create project rule: %v", err)
	}

	// Project-level should take priority
	rule, err := s.FindAccessRuleForSite("findsite", "myapp")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite: %v", err)
	}
	if rule == nil {
		t.Fatal("expected non-nil rule for project")
	}
	if rule.ProjectID == nil {
		t.Fatal("expected project-level rule")
	}
	if rule.PasswordHash != "projpass" {
		t.Errorf("expected projpass, got %s", rule.PasswordHash)
	}

	// For a project without its own rule, should fall back to subdomain-level
	_, _ = s.CreateProject(sub.ID, "other")
	rule, err = s.FindAccessRuleForSite("findsite", "other")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite fallback: %v", err)
	}
	if rule == nil {
		t.Fatal("expected non-nil rule for fallback")
	}
	if rule.ProjectID != nil {
		t.Error("expected subdomain-level rule as fallback")
	}
	if rule.PasswordHash != "subpass" {
		t.Errorf("expected subpass, got %s", rule.PasswordHash)
	}
}

func TestFindAccessRuleForSiteNoRule(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("norule@example.com", "hash", "token-norule")
	sub, _ := s.CreateSubdomain(user.ID, "norulesite")
	_, _ = s.CreateProject(sub.ID, "app")

	rule, err := s.FindAccessRuleForSite("norulesite", "app")
	if err != nil {
		t.Fatalf("FindAccessRuleForSite: %v", err)
	}
	if rule != nil {
		t.Errorf("expected nil for unprotected site, got %+v", rule)
	}
}

func TestHasAccessRules(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("has@example.com", "hash", "token-has")
	sub, _ := s.CreateSubdomain(user.ID, "hassite")

	has, err := s.HasAccessRules(sub.ID)
	if err != nil {
		t.Fatalf("HasAccessRules: %v", err)
	}
	if has {
		t.Error("expected false before creating rule")
	}

	_, err = s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "pass", 3600, false, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}

	has, err = s.HasAccessRules(sub.ID)
	if err != nil {
		t.Fatalf("HasAccessRules after create: %v", err)
	}
	if !has {
		t.Error("expected true after creating rule")
	}
}

func TestAccessRuleCascadeDeleteProject(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("cascade@example.com", "hash", "token-cascade")
	sub, _ := s.CreateSubdomain(user.ID, "cascadesite")
	proj, _ := s.CreateProject(sub.ID, "app")

	projID := proj.ID
	_, err := s.CreateOrUpdateAccessRule(sub.ID, &projID, nil, "pass", 3600, false, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
	}

	// Delete the project; access rule should be cascade-deleted
	if err := s.DeleteProject(sub.ID, "app"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}

	got, err := s.GetAccessRule(sub.ID, &projID)
	if err != nil {
		t.Fatalf("GetAccessRule after cascade: %v", err)
	}
	if got != nil {
		t.Error("expected nil after cascade delete of project")
	}
}
