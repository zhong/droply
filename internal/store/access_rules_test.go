package store

import (
	"context"
	"errors"
	"testing"
)

func TestAccessRuleCanceledOperationsPreserveRule(t *testing.T) {
	s := newTestStore(t)
	projectID := deploymentProject(t, s)
	project, err := s.getProjectByID(projectID)
	if err != nil {
		t.Fatal(err)
	}
	input := AccessRuleInput{SubdomainID: project.SubdomainID, PasswordHash: "keep", SessionTTL: 3600}
	if _, err := s.PutAccessRule(t.Context(), input); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	input.PasswordHash = "replace"
	_, putErr := s.PutAccessRule(ctx, input)
	_, getErr := s.GetAccessRule(ctx, input.SubdomainID, nil)
	deleteErr := s.DeleteAccessRule(ctx, input.SubdomainID, nil)
	_, findErr := s.FindAccessRuleForSite(ctx, "alice", "site")
	_, hasErr := s.HasAccessRules(ctx, input.SubdomainID)
	for _, err := range []error{putErr, getErr, deleteErr, findErr, hasErr} {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	}
	rule, err := s.GetAccessRule(t.Context(), input.SubdomainID, nil)
	if err != nil || rule.PasswordHash != "keep" {
		t.Fatalf("canceled operation changed rule: %+v %v", rule, err)
	}
}

func TestAccessRuleNullableValues(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		ips, password, users any
		hasPassword          bool
	}{
		{"NULL", nil, nil, nil, false},
		{"empty", "[]", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			projectID := deploymentProject(t, s)
			project, err := s.getProjectByID(projectID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO access_rules(subdomain_id,allowed_ips,password_hash,allowed_wework_users) VALUES(?,?,?,?)`, project.SubdomainID, tc.ips, tc.password, tc.users); err != nil {
				t.Fatal(err)
			}
			rule, err := s.GetAccessRule(t.Context(), project.SubdomainID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if rule.ProjectID != nil || len(rule.AllowedIPs) != 0 || len(rule.AllowedWeWorkUsers) != 0 || rule.HasPassword != tc.hasPassword {
				t.Fatalf("nullable values changed: %+v", rule)
			}
		})
	}
}

func TestAccessRuleMalformedProjectDoesNotFallBack(t *testing.T) {
	s := newTestStore(t)
	projectID := deploymentProject(t, s)
	project, err := s.getProjectByID(projectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PutAccessRule(t.Context(), AccessRuleInput{SubdomainID: project.SubdomainID, PasswordHash: "parent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO access_rules(subdomain_id,project_id,allowed_ips) VALUES(?,?,'invalid-json')`, project.SubdomainID, projectID); err != nil {
		t.Fatal(err)
	}
	var subdomain string
	if err := s.db.QueryRow(`SELECT name FROM subdomains WHERE id=?`, project.SubdomainID).Scan(&subdomain); err != nil {
		t.Fatal(err)
	}
	if rule, err := s.FindAccessRuleForSite(t.Context(), subdomain, project.Name); err == nil || rule != nil {
		t.Fatalf("corrupt project rule fell back: %+v %v", rule, err)
	}
}
