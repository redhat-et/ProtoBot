package main

import (
	"os"

	"github.com/redhat-et/protobot/validator/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
