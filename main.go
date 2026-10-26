package main

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/cli"
	"os"
)

var version = "0.1.0"

func main() {
	cli.Version = version
	os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
