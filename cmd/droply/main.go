package main

import (
	"os"

	"github.com/zhong/droply/internal/cli"
)

var version string

func main() {
	if err := cli.NewRootCmd(version).Execute(); err != nil {
		os.Exit(1)
	}
}
