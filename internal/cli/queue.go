package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newQueueCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "queue",
		Short: "Inspect background worker queue depth and in-flight job processing",
		RunE:  runQueue,
	}
}

func runQueue(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	pending, inFlight, err := client.GetQueueStats(ctx)
	if err != nil {
		return &statusError{code: 1, err: err}
	}

	queueDepth := pending + inFlight
	status := "HEALTHY"
	exitCode := 0
	if queueDepth >= 1000 {
		status = "BACKLOG"
		exitCode = 2
	}

	if strings.ToLower(opts.Output) == "json" {
		if err := RenderJSON(cmd.OutOrStdout(), map[string]any{
			"queue_depth": queueDepth,
			"pending":     pending,
			"in_flight":   inFlight,
			"status":      status,
		}); err != nil {
			return err
		}
		if exitCode != 0 {
			return &statusError{code: exitCode, err: fmt.Errorf("queue depth exceeded backlog threshold: %d", queueDepth)}
		}
		return nil
	}

	out := cmd.OutOrStdout()
	isTerm := IsTerminal(out)
	headers := []string{"QUEUE DEPTH", "PENDING", "IN-FLIGHT", "STATUS"}
	rows := [][]string{
		{
			fmt.Sprintf("%d", queueDepth),
			fmt.Sprintf("%d", pending),
			fmt.Sprintf("%d", inFlight),
			FormatStatus(status, isTerm),
		},
	}

	if err := RenderTable(out, headers, rows); err != nil {
		return err
	}

	if exitCode != 0 {
		return &statusError{code: exitCode, err: fmt.Errorf("queue depth exceeded backlog threshold: %d", queueDepth)}
	}
	return nil
}
