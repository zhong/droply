package store

import (
	"reflect"
	"testing"
)

func TestAccessRuleWithWeWork(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("we@example.com", "hash", "token-we")
	sub, _ := s.CreateSubdomain(user.ID, "wesite")

	allowed := []string{"alice", "bob"}
	rule, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: sub.ID, ProjectID: nil, AllowedIPs: nil, PasswordHash: "", SessionTTL: 3600, WeWorkEnabled: true, AllowedWeWorkUsers: allowed})
	if err != nil {
		t.Fatalf("PutAccessRule: %v", err)
	}
	if !rule.WeWorkEnabled {
		t.Error("expected WeWorkEnabled=true")
	}
	if !reflect.DeepEqual(rule.AllowedWeWorkUsers, allowed) {
		t.Errorf("allowed users mismatch: got %v want %v", rule.AllowedWeWorkUsers, allowed)
	}
	if rule.HasPassword {
		t.Error("expected HasPassword=false")
	}

	// Round-trip via GetAccessRule.
	got, err := s.GetAccessRule(t.Context(), sub.ID, nil)
	if err != nil {
		t.Fatalf("GetAccessRule: %v", err)
	}
	if !got.WeWorkEnabled {
		t.Error("expected WeWorkEnabled=true after re-read")
	}
	if !reflect.DeepEqual(got.AllowedWeWorkUsers, allowed) {
		t.Errorf("allowed users after re-read: got %v want %v", got.AllowedWeWorkUsers, allowed)
	}
}

func TestAccessRuleWeWorkAnyMember(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("any@example.com", "hash", "token-any")
	sub, _ := s.CreateSubdomain(user.ID, "anysite")

	// wework_enabled=true with no allow-list → any corp member.
	rule, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: sub.ID, ProjectID: nil, AllowedIPs: nil, PasswordHash: "", SessionTTL: 3600, WeWorkEnabled: true, AllowedWeWorkUsers: nil})
	if err != nil {
		t.Fatalf("PutAccessRule: %v", err)
	}
	if !rule.WeWorkEnabled {
		t.Error("expected WeWorkEnabled=true")
	}
	if len(rule.AllowedWeWorkUsers) != 0 {
		t.Errorf("expected empty allow-list, got %v", rule.AllowedWeWorkUsers)
	}
}

func TestAccessRuleWeWorkCombinedWithPassword(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("combo@example.com", "hash", "token-combo")
	sub, _ := s.CreateSubdomain(user.ID, "combosite")

	rule, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: sub.ID, ProjectID: nil, AllowedIPs: []string{"10.0.0.1"}, PasswordHash: "bcrypt-hash", SessionTTL: 7200, WeWorkEnabled: true, AllowedWeWorkUsers: []string{"alice"}})
	if err != nil {
		t.Fatalf("PutAccessRule: %v", err)
	}
	if !rule.HasPassword {
		t.Error("expected HasPassword=true")
	}
	if !rule.WeWorkEnabled {
		t.Error("expected WeWorkEnabled=true")
	}
	if len(rule.AllowedIPs) != 1 {
		t.Errorf("expected 1 IP, got %v", rule.AllowedIPs)
	}
}

func TestAccessRuleUpdateDisablesWeWork(t *testing.T) {
	s := newTestStore(t)

	user, _ := s.CreateUser("dis@example.com", "hash", "token-dis")
	sub, _ := s.CreateSubdomain(user.ID, "dissite")

	// Enable, then disable.
	_, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: sub.ID, ProjectID: nil, AllowedIPs: nil, PasswordHash: "", SessionTTL: 3600, WeWorkEnabled: true, AllowedWeWorkUsers: []string{"alice"}})
	if err != nil {
		t.Fatalf("first PutAccessRule: %v", err)
	}
	rule, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: sub.ID, ProjectID: nil, AllowedIPs: nil, PasswordHash: "pass-hash", SessionTTL: 3600, WeWorkEnabled: false, AllowedWeWorkUsers: nil})
	if err != nil {
		t.Fatalf("update PutAccessRule: %v", err)
	}
	if rule.WeWorkEnabled {
		t.Error("expected WeWorkEnabled=false after disable")
	}
	if len(rule.AllowedWeWorkUsers) != 0 {
		t.Errorf("expected empty allow-list, got %v", rule.AllowedWeWorkUsers)
	}
	if !rule.HasPassword {
		t.Error("expected HasPassword=true")
	}
}
