package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
)

const (
	ansiReset  = "\033[0m"
	ansiRed    = "\033[31m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiBold   = "\033[1m"
)

// FormatStatus returns colored status text if terminal colors are enabled.
func FormatStatus(status string, isTerminal bool) string {
	if !isTerminal || os.Getenv("NO_COLOR") != "" {
		return status
	}

	lower := strings.ToLower(status)
	switch lower {
	case "ok", "healthy", "success", "completed":
		return fmt.Sprintf("%s%s%s%s", ansiBold, ansiGreen, status, ansiReset)
	case "pending", "processing", "in_flight":
		return fmt.Sprintf("%s%s%s%s", ansiBold, ansiYellow, status, ansiReset)
	case "failed", "degraded", "backlog", "unhealthy", "dlq":
		return fmt.Sprintf("%s%s%s%s", ansiBold, ansiRed, status, ansiReset)
	default:
		return status
	}
}

// Truncate shortens a string to maxLen and appends an ellipsis.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 0 {
		return ""
	}
	return s[:maxLen] + "..."
}

// RenderTable prints aligned tabular data using standard tabwriter.
func RenderTable(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 4, ' ', 0)

	// Write Header row
	headerLine := strings.Join(headers, "\t")
	if _, err := fmt.Fprintln(tw, headerLine); err != nil {
		return fmt.Errorf("failed to write table headers: %w", err)
	}

	// Write Data rows
	for _, row := range rows {
		rowLine := strings.Join(row, "\t")
		if _, err := fmt.Fprintln(tw, rowLine); err != nil {
			return fmt.Errorf("failed to write table row: %w", err)
		}
	}

	return tw.Flush()
}

// RenderJSON serializes data to pretty-printed JSON.
func RenderJSON(w io.Writer, data any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	return nil
}
