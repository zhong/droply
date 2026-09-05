package store

import "fmt"

// Run every step on every startup: these migrations are intentionally reentrant.
// The order and error prefixes also apply when resuming an interrupted upgrade.
func (s *SQLiteStore) runMigrations() error {
	for _, migration := range []struct {
		name string
		run  func() error
	}{
		{"migrate", s.migrate},
		{"migrate wework", s.migrateWeWork},
		{"migrate domain verification", s.migrateDomainVerification},
		{"migrate deployments", s.migrateDeployments},
		{"migrate pages", s.migratePages},
		{"migrate project tokens", s.migrateProjectTokens},
		{"migrate identity", s.migrateIdentity},
		{"migrate members", s.migrateMembers},
		{"migrate audit", s.migrateAudit},
		{"migrate console sessions", s.migrateConsoleSessions},
	} {
		if err := migration.run(); err != nil {
			return fmt.Errorf("%s: %w", migration.name, err)
		}
	}
	return nil
}
