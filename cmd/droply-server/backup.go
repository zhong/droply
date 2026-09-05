package main

import (
	"context"
	"errors"
	"flag"
	"github.com/zhong/droply/internal/backup"
)

type backupPaths []string

func (p *backupPaths) String() string { return "" }
func (p *backupPaths) Set(value string) error {
	if value == "" {
		return errors.New("path cannot be empty")
	}
	*p = append(*p, value)
	return nil
}

// Backup is deliberately offline: the package takes the same lock as serving.
func runBackupCommand(args []string) (bool, error) {
	if len(args) == 0 || (args[0] != "backup" && args[0] != "restore") {
		return false, nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	data := flags.String("data-dir", "/data/droply", "Installation directory (must be new for restore)")
	if args[0] == "backup" {
		output := flags.String("output", "", "New private tar.gz backup file")
		hmac := flags.String("hmac-key-file", "", "File containing exact active HMAC override bytes")
		var configs, includes backupPaths
		flags.Var(&configs, "config", "Actual service configuration file; repeat for multiple files")
		flags.Var(&includes, "include", "External certificate directory or other required file; repeatable")
		if err := flags.Parse(args[1:]); err != nil {
			return true, err
		}
		if flags.NArg() != 0 {
			return true, errors.New("unexpected positional arguments")
		}
		return true, backup.Create(context.Background(), backup.Config{DataDir: *data, Output: *output, Configs: configs, Include: includes, HMACFile: *hmac})
	}
	input := flags.String("input", "", "Backup tar.gz file")
	maxBytes := flags.Int64("max-bytes", 64<<30, "Maximum extracted backup bytes")
	maxFiles := flags.Int("max-files", 100000, "Maximum extracted backup entries")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, errors.New("unexpected positional arguments")
	}
	if *maxBytes <= 0 || *maxFiles <= 0 {
		return true, errors.New("restore limits must be positive")
	}
	return true, backup.Restore(context.Background(), backup.RestoreConfig{Input: *input, DataDir: *data, MaxBytes: *maxBytes, MaxFiles: *maxFiles})
}
