package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// SchemaVersion is the newest database format understood by this binary.
// M0–M2 used version zero; their migrations are detected from table columns.
const SchemaVersion = 3

// prepareSchemaUpgrade runs before any schema or journal configuration changes.
// The caller must hold the data directory lock when opening a server database.
func prepareSchemaUpgrade(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version < 0 || version > SchemaVersion {
		return fmt.Errorf("unsupported database schema version %d (maximum %d); restore the matching pre-upgrade backup to downgrade", version, SchemaVersion)
	}
	if version == SchemaVersion {
		return nil
	}
	var tables int
	if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		return nil
	}
	var seq int
	var name, databasePath string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &databasePath); err != nil {
		return err
	}
	// In-memory databases have no durable state to protect.
	if databasePath == "" {
		return nil
	}
	dir := filepath.Join(filepath.Dir(databasePath), "upgrade-backups")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create upgrade backup directory: %w", err)
	}
	f, err := os.CreateTemp(dir, fmt.Sprintf("schema-%d-before-%d-*.db", version, SchemaVersion))
	if err != nil {
		return fmt.Errorf("create upgrade backup: %w", err)
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		return err
	}
	// VACUUM INTO includes committed WAL contents; copying the main file would not.
	if _, err := db.Exec("VACUUM INTO ?", path); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("snapshot database before upgrade: %w", err)
	}
	snapshot, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	syncErr := snapshot.Sync()
	closeErr := snapshot.Close()
	if syncErr != nil {
		return fmt.Errorf("sync upgrade backup: %w", syncErr)
	}
	if closeErr != nil {
		return closeErr
	}
	// Persist both the snapshot entry and a newly created backup directory before
	// allowing any mutation of the original database.
	for _, directory := range []string{dir, filepath.Dir(dir)} {
		f, err := os.Open(directory)
		if err != nil {
			return err
		}
		syncErr := f.Sync()
		closeErr := f.Close()
		if syncErr != nil {
			return fmt.Errorf("sync upgrade backup directory: %w", syncErr)
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// Health checks that SQLite can execute a query without returning database details.
func (s *SQLiteStore) Health(ctx context.Context) error {
	var value int
	return s.db.QueryRowContext(ctx, "SELECT 1").Scan(&value)
}
