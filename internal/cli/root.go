package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// ExitCodeError represents an error carrying a specific POSIX exit code.
type ExitCodeError interface {
	error
	ExitCode() int
}

type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string {
	if e.err != nil {
		return e.err.Error()
	}
	return fmt.Sprintf("exit code %d", e.code)
}

func (e *statusError) ExitCode() int {
	return e.code
}

func (e *statusError) Unwrap() error {
	return e.err
}

func errorsAsExitCode(err error, target *ExitCodeError) bool {
	var ec ExitCodeError
	if errors.As(err, &ec) {
		*target = ec
		return true
	}
	return false
}

// Global flag variables
var (
	flagServer  string
	flagAPIKey  string
	flagOutput  string
	flagTimeout time.Duration
)

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

// NewRootCmd initializes the root Cobra command and its persistent flags.
func NewRootCmd() *cobra.Command {
	defaultServer := getEnvOrDefault("RELAY_SERVER_URL", "http://127.0.0.1:8080")
	defaultAPIKey := os.Getenv("RELAY_API_KEY")
	defaultOutput := getEnvOrDefault("RELAY_OUTPUT", "table")
	defaultTimeout := 10 * time.Second
	if envTimeout := os.Getenv("RELAY_TIMEOUT"); envTimeout != "" {
		if d, err := time.ParseDuration(envTimeout); err == nil {
			defaultTimeout = d
		}
	}

	cmd := &cobra.Command{
		Use:   "relay-cli",
		Short: "Simple Relay command-line inspection and operational management tool",
		Long:  "relay-cli enables operators and automated pipelines to inspect health, monitor queue depth, and triage/replay Dead Letter Queue messages.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().StringVarP(&flagServer, "server", "s", defaultServer, "Simple Relay daemon base URL (env: RELAY_SERVER_URL)")
	cmd.PersistentFlags().StringVarP(&flagAPIKey, "api-key", "k", defaultAPIKey, "Admin API key for authentication (env: RELAY_API_KEY)")
	cmd.PersistentFlags().StringVarP(&flagOutput, "output", "o", defaultOutput, "Output format: table or json (env: RELAY_OUTPUT)")
	cmd.PersistentFlags().DurationVar(&flagTimeout, "timeout", defaultTimeout, "HTTP client timeout (env: RELAY_TIMEOUT)")

	// Register subcommands
	cmd.AddCommand(newHealthCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newQueueCmd())
	cmd.AddCommand(newDLQCmd())

	return cmd
}

// Execute runs the default root command attached to os.Stdout and os.Stderr.
func Execute() error {
	cmd := NewRootCmd()
	err := cmd.Execute()
	if err != nil {
		var ec ExitCodeError
		if errorsAsExitCode(err, &ec) {
			if ec.ExitCode() != 2 && ec.Error() != "" {
				fmt.Fprintf(os.Stderr, "Error: %v\n", ec)
			}
			os.Exit(ec.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

// ExecuteCommand executes root command with custom args and IO buffers (for tests).
func ExecuteCommand(args []string, out, errOut io.Writer) error {
	cmd := NewRootCmd()
	cmd.SetArgs(args)
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	return cmd.Execute()
}

func getClient() *RelayClient {
	return NewClient(flagServer, flagAPIKey, flagTimeout)
}
