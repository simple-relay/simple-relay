package main

import (
	"os"

	"github.com/simple-relay/simple-relay/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
