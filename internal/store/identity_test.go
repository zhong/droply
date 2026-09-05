package store

import (
	"errors"
	"testing"
	"time"
)

func TestInvitationsConsumeAtomicallyAndNeverElevate(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	admin, err := s.BootstrapAdmin(ctx, "admin@test", "hash", "admin-token")
	if err != nil || !admin.IsAdmin {
		t.Fatalf("bootstrap %+v %v", admin, err)
	}
	if _, err := s.BootstrapAdmin(ctx, "other@test", "hash", "other-token"); !errors.Is(err, ErrAdministratorExists) {
		t.Fatal(err)
	}
	invite, raw, err := s.CreateInvitation(ctx, admin.ID, "member@test", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := s.db.QueryRow(`SELECT digest FROM invitations WHERE id=?`, invite.ID).Scan(&stored); err != nil || stored == raw {
		t.Fatal("raw invitation persisted")
	}
	if _, err := s.RegisterInvited(ctx, "wrong@test", "hash", "token", raw); !errors.Is(err, ErrInvitation) {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_invite_user BEFORE INSERT ON users BEGIN SELECT RAISE(ABORT,"fail"); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterInvited(ctx, "member@test", "hash", "token", raw); err == nil {
		t.Fatal("injected failure accepted")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_invite_user`); err != nil {
		t.Fatal(err)
	}
	member, err := s.RegisterInvited(ctx, "member@test", "hash", "token", raw)
	if err != nil || member.IsAdmin {
		t.Fatalf("member %+v %v", member, err)
	}
	if _, err := s.RegisterInvited(ctx, "member@test", "hash", "token2", raw); !errors.Is(err, ErrInvitation) {
		t.Fatal(err)
	}
	for _, reason := range []string{"expired", "revoked"} {
		invite, raw, err := s.CreateInvitation(ctx, admin.ID, reason+"@test", time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if reason == "expired" {
			_, err = s.db.Exec(`UPDATE invitations SET expires_at=? WHERE id=?`, time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), invite.ID)
		} else {
			err = s.RevokeInvitation(ctx, invite.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.RegisterInvited(ctx, reason+"@test", "hash", reason, raw); !errors.Is(err, ErrInvitation) {
			t.Fatal(err)
		}
	}
}
func TestExistingUserAdminClaimIsExplicit(t *testing.T) {
	s := newTestStore(t)
	user, err := s.CreateUser("existing@test", "hash", "existing")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetUserByToken("existing")
	if err != nil || got.IsAdmin {
		t.Fatal("ordinary account auto promoted")
	}
	admin, err := s.ClaimAdmin(t.Context(), user.Email)
	if err != nil || !admin.IsAdmin || admin.ID != user.ID {
		t.Fatalf("claim %+v %v", admin, err)
	}
	got, err = s.GetUserByToken("existing")
	if err != nil || !got.IsAdmin {
		t.Fatal("existing login identity not preserved")
	}
}
