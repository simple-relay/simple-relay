package cli

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

func newDLQCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dlq",
		Short: "Inspect and replay Dead Letter Queue messages",
	}

	cmd.AddCommand(newDLQListCmd())
	cmd.AddCommand(newDLQInspectCmd())
	cmd.AddCommand(newDLQReplayCmd())
	cmd.AddCommand(newDLQReplayAllCmd())

	return cmd
}

func newDLQListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List failed requests preserved in the Dead Letter Queue",
		RunE:  runDLQList,
	}
	cmd.Flags().Int("limit", 50, "Maximum number of DLQ records to fetch")
	return cmd
}

func runDLQList(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)

	limit, _ := cmd.Flags().GetInt("limit")
	items, _, err := client.ListDLQ(ctx, limit)
	if err != nil {
		return &statusError{code: 1, err: err}
	}

	if strings.ToLower(opts.Output) == "json" {
		return RenderJSON(cmd.OutOrStdout(), items)
	}

	if len(items) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Dead Letter Queue is empty.")
		return nil
	}

	// Resolve route names only when formatting tabular output and DLQ has items
	routesMap, _ := client.ListRoutes(ctx)
	if routesMap == nil {
		routesMap = make(map[string]string)
	}

	headers := []string{"REQUEST ID", "ROUTE", "ATTEMPTS", "FAILED AT", "LAST ERROR"}
	rows := make([][]string, 0, len(items))

	for _, item := range items {
		routeName := routesMap[item.RouteID]
		if routeName == "" {
			routeName = Truncate(item.RouteID, 10)
		}

		lastErr := "-"
		if item.LastError != nil && *item.LastError != "" {
			lastErr = Truncate(SanitizeTerminal(*item.LastError), 45)
		}

		rows = append(rows, []string{
			item.ID,
			routeName,
			strconv.Itoa(item.AttemptsCount),
			item.UpdatedAt.Format("2006-01-02 15:04:05"),
			lastErr,
		})
	}

	return RenderTable(cmd.OutOrStdout(), headers, rows)
}

func newDLQInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <request_id>",
		Short: "Inspect comprehensive metadata and attempt history of a failed request",
		Args:  cobra.ExactArgs(1),
		RunE:  runDLQInspect,
	}
}

func runDLQInspect(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	detail, err := client.InspectRequest(ctx, reqID)
	if err != nil {
		return &statusError{code: 1, err: err}
	}

	if strings.ToLower(opts.Output) == "json" {
		return RenderJSON(cmd.OutOrStdout(), detail)
	}

	out := cmd.OutOrStdout()
	isTerm := IsTerminal(out)
	fmt.Fprintf(out, "Request ID:    %s\n", detail.ID)
	fmt.Fprintf(out, "Route ID:      %s\n", detail.RouteID)
	fmt.Fprintf(out, "Method:        %s\n", detail.Method)
	fmt.Fprintf(out, "Destination:   %s\n", detail.URL)
	fmt.Fprintf(out, "Status:        %s\n", FormatStatus(detail.Status, isTerm))
	fmt.Fprintf(out, "Created At:    %s\n", detail.CreatedAt)

	lastErr := "-"
	if detail.LastError != nil && *detail.LastError != "" {
		lastErr = SanitizeTerminal(*detail.LastError)
	}
	fmt.Fprintf(out, "Last Error:    %s\n", lastErr)
	fmt.Fprintln(out)

	if len(detail.Attempts) > 0 {
		fmt.Fprintln(out, "Attempt History:")
		headers := []string{"#", "ATTEMPTED AT", "STATUS CODE", "DURATION", "ERROR"}
		rows := make([][]string, 0, len(detail.Attempts))
		for _, att := range detail.Attempts {
			errStr := "-"
			if att.ErrorMessage != nil && *att.ErrorMessage != "" {
				errStr = Truncate(SanitizeTerminal(*att.ErrorMessage), 40)
			}
			statusStr := "-"
			if sc := att.GetStatusCode(); sc > 0 {
				statusStr = strconv.Itoa(sc)
			}
			timeStr := "-"
			if t := att.GetAttemptedAt(); !t.IsZero() {
				timeStr = t.Format("2006-01-02 15:04:05")
			}
			rows = append(rows, []string{
				strconv.Itoa(att.AttemptNumber),
				timeStr,
				statusStr,
				fmt.Sprintf("%dms", att.DurationMS),
				errStr,
			})
		}
		if err := RenderTable(out, headers, rows); err != nil {
			return err
		}
	}

	return nil
}

func newDLQReplayCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "replay <request_id>",
		Short: "Requeue a specific DLQ message for delivery retry",
		Args:  cobra.ExactArgs(1),
		RunE:  runDLQReplay,
	}
}

func runDLQReplay(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	if err := client.ReplayDLQ(ctx, reqID); err != nil {
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

func newDLQReplayAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "replay-all",
		Short: "Requeue all Dead Letter Queue messages for delivery retry",
		RunE:  runDLQReplayAll,
	}
	cmd.Flags().BoolP("yes", "y", false, "Bypass interactive confirmation prompt")
	return cmd
}

func runDLQReplayAll(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	opts := GetOptions(ctx)
	client := getClient(ctx)
	yes, _ := cmd.Flags().GetBool("yes")

	// Check interactive TTY
	isTerm := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())

	if !yes {
		if !isTerm {
			return &statusError{
				code: 1,
				err:  fmt.Errorf("non-interactive terminal detected. Must specify --yes (-y) to confirm bulk replay"),
			}
		}

		// In TTY: inspect total DLQ items to prompt user
		_, total, err := client.ListDLQ(ctx, 1)
		if err != nil {
			return &statusError{code: 1, err: err}
		}

		prompt := fmt.Sprintf("Found %d items in DLQ. Requeue all for delivery? [y/N]: ", total)
		fmt.Fprint(cmd.OutOrStdout(), prompt)

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if strings.ToLower(input) != "y" && strings.ToLower(input) != "yes" {
			fmt.Fprintln(cmd.OutOrStdout(), "Operation canceled.")
			return nil
		}
	}

	replayedCount, err := client.ReplayAllDLQ(ctx)
	if err != nil {
		return &statusError{code: 1, err: err}
	}

	if strings.ToLower(opts.Output) == "json" {
		return RenderJSON(cmd.OutOrStdout(), map[string]any{
			"status":         "requeued",
			"replayed_count": replayedCount,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Successfully requeued %d DLQ requests for delivery.\n", replayedCount)
	return nil
}
