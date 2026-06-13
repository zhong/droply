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
	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "", 3600, true, allowed)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
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
	got, err := s.GetAccessRule(sub.ID, nil)
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
	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "", 3600, true, nil)
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
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

	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, []string{"10.0.0.1"}, "bcrypt-hash", 7200, true, []string{"alice"})
	if err != nil {
		t.Fatalf("CreateOrUpdateAccessRule: %v", err)
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
	_, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "", 3600, true, []string{"alice"})
	if err != nil {
		t.Fatalf("first CreateOrUpdateAccessRule: %v", err)
	}
	rule, err := s.CreateOrUpdateAccessRule(sub.ID, nil, nil, "pass-hash", 3600, false, nil)
	if err != nil {
		t.Fatalf("update CreateOrUpdateAccessRule: %v", err)
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
