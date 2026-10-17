package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/simple-relay/simple-relay/internal/cli"
	"github.com/simple-relay/simple-relay/internal/config"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(ctx context.Context, cfg *config.Config, logger *slog.Logger) error {
	return cli.RunServer(ctx, cfg, logger)
}
