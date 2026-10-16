package main

import (
	"os"

	guard "github.com/redhat-et/protobot/drafting-table"
)

func main() {
	os.Exit(guard.Run(os.Args[1:], os.Stdin, os.Stderr))
}
