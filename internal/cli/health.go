package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newHealthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "health",
		Short: "Inspect relay daemon health and round-trip latency",
		RunE:  runHealth,
	}
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Inspect relay daemon health and round-trip latency (alias for health)",
		RunE:  runHealth,
	}
}

func runHealth(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	health, latency, err := client.Health(ctx)

	isJSON := strings.ToLower(opts.Output) == "json"

	out := cmd.OutOrStdout()
	isTerm := IsTerminal(out)

	if err != nil {
		if health != nil {
			// Server responded with non-OK / degraded status
			if isJSON {
				_ = RenderJSON(out, map[string]any{
					"status":     health.Status,
					"latency_ms": latency.Milliseconds(),
					"url":        opts.Server,
					"error":      err.Error(),
				})
			} else {
				headers := []string{"STATUS", "LATENCY", "SERVER TIME", "URL"}
				rows := [][]string{
					{
						FormatStatus(health.Status, isTerm),
						fmt.Sprintf("%.2fms", float64(latency.Microseconds())/1000.0),
						health.Timestamp,
						opts.Server,
					},
				}
				_ = RenderTable(out, headers, rows)
			}
			return &statusError{code: 2, err: err}
		}
		// Connection failure or other client error
		return &statusError{code: 1, err: err}
	}

	if isJSON {
		return RenderJSON(out, map[string]any{
			"status":     health.Status,
			"latency_ms": latency.Milliseconds(),
			"timestamp":  health.Timestamp,
			"url":        opts.Server,
		})
	}

	headers := []string{"STATUS", "LATENCY", "SERVER TIME", "URL"}
	rows := [][]string{
		{
			FormatStatus(health.Status, isTerm),
			fmt.Sprintf("%.2fms", float64(latency.Microseconds())/1000.0),
			health.Timestamp,
			opts.Server,
		},
	}
	return RenderTable(out, headers, rows)
}
