package main

import (
	"context"
	"github.com/CamiloCaceres/lolad/internal/cli"
	"os"
)

func main() { os.Exit(cli.Run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
