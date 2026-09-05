package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zhong/droply/internal/hosting"
	"github.com/zhong/droply/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Administrator initialization is a local, locked operation, never an HTTP route.
func runIdentityCommand(ctx context.Context, args []string, out io.Writer) (bool, error) {
	if len(args) == 0 || (args[0] != "init-admin" && args[0] != "claim-admin") {
		return false, nil
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dataDir := flags.String("data-dir", "/data/droply", "Installation data directory; stop the server first")
	email := flags.String("email", "", "Administrator email")
	passwordFile := flags.String("password-file", "", "Private file containing the initial password (init-admin only)")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 || strings.TrimSpace(*email) == "" {
		return true, errors.New("--email is required; no positional arguments are accepted")
	}
	var hash []byte
	if args[0] == "init-admin" {
		if *passwordFile == "" {
			return true, errors.New("--password-file is required")
		}
		info, err := os.Lstat(*passwordFile)
		if err != nil {
			return true, errors.New("cannot read password file")
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024 {
			return true, errors.New("password file must be a private regular file (mode 0600)")
		}
		data, err := os.ReadFile(*passwordFile)
		if err != nil {
			return true, errors.New("cannot read password file")
		}
		password := strings.TrimRight(string(data), "\r\n")
		if len(password) < 8 || len(password) > 72 {
			return true, errors.New("password must contain 8 to 72 bytes")
		}
		hash, err = bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return true, errors.New("cannot hash password")
		}
	} else if *passwordFile != "" {
		return true, errors.New("claim-admin does not change a password")
	}
	if err := os.MkdirAll(*dataDir, 0700); err != nil {
		return true, err
	}
	lock, err := hosting.LockDataDirectory(*dataDir)
	if err != nil {
		return true, err
	}
	defer lock.Close()
	st, err := store.NewSQLiteStore(filepath.Join(*dataDir, "droply.db"))
	if err != nil {
		return true, err
	}
	defer st.Close()
	if args[0] == "init-admin" {
		if _, err := st.BootstrapAdmin(ctx, strings.TrimSpace(*email), string(hash), "dp_"+rand.Text()); err != nil {
			return true, fmt.Errorf("initialize administrator: %w", err)
		}
	} else {
		if _, err := st.ClaimAdmin(ctx, strings.TrimSpace(*email)); err != nil {
			return true, fmt.Errorf("claim administrator: %w", err)
		}
	}
	_, err = fmt.Fprintln(out, "Administrator configured. Start the server and log in with the account password.")
	return true, err
}
