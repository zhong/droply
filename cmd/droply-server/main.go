package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if handled, err := runBackupCommand(args); handled {
		return err
	}
	if handled, err := runIdentityCommand(ctx, args, os.Stdout); handled {
		return err
	}
	cfg, err := parseServerConfig(args)
	if err != nil {
		return err
	}
	tlsConfig, err := cfg.validate()
	if err != nil {
		return err
	}
	return runServer(ctx, cfg, tlsConfig)
}
