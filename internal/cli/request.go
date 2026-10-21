package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newRequestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "request",
		Aliases: []string{"requests"},
		Short:   "Inspect and replay requests",
	}

	cmd.AddCommand(newRequestReplayCmd())
	return cmd
}

func newRequestReplayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "replay <request_id>",
		Short: "Requeue any request (success, failed, or dlq) for immediate redelivery",
		Args:  cobra.ExactArgs(1),
		RunE:  runRequestReplay,
	}
}

func runRequestReplay(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	if err := client.ReplayRequest(ctx, reqID); err != nil {
		return &statusError{code: 1, err: err}
	}

	if strings.ToLower(opts.Output) == "json" {
		return RenderJSON(cmd.OutOrStdout(), map[string]any{
			"status":     "requeued",
			"request_id": reqID,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully requeued request %s for delivery.\n", reqID)
	return nil
}
