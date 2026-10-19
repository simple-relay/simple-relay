package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

// GlobalOptions encapsulates persistent CLI configuration flags.
type GlobalOptions struct {
	Server  string
	APIKey  string
	Output  string
	Timeout time.Duration
}

type optionsKey struct{}

func contextWithOptions(ctx context.Context, opts *GlobalOptions) context.Context {
	return context.WithValue(ctx, optionsKey{}, opts)
}

// GetOptions extracts GlobalOptions from context, or returns sensible defaults.
func GetOptions(ctx context.Context) *GlobalOptions {
	if opts, ok := ctx.Value(optionsKey{}).(*GlobalOptions); ok && opts != nil {
		return opts
	}
	return &GlobalOptions{
		Server:  "http://127.0.0.1:8080",
		Output:  "table",
		Timeout: 10 * time.Second,
	}
}

func getClient(ctx context.Context) *RelayClient {
	opts := GetOptions(ctx)
	return NewClient(opts.Server, opts.APIKey, opts.Timeout)
}

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

var knownServerFlags = map[string]bool{
	"host":               true,
	"port":               true,
	"db":                 true,
	"log-level":          true,
	"workers":            true,
	"dispatcher-buffer":  true,
	"dispatcher-poll-ms": true,
	"cleaner-interval":   true,
	"drain-timeout":      true,
	"trust-proxy":        true,
}

func normalizeArgs(args []string) []string {
	res := make([]string, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// Stop normalizing after the POSIX end-of-flags delimiter "--"
		if arg == "--" {
			copy(res[i:], args[i:])
			break
		}

		// Only rewrite single-dash aliases for known server flags (e.g. -host, -port, -db)
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && len(arg) > 2 {
			flagPart := strings.TrimPrefix(arg, "-")
			flagName := strings.SplitN(flagPart, "=", 2)[0]
			if knownServerFlags[flagName] {
				res[i] = "-" + arg
				continue
			}
		}
		res[i] = arg
	}
	return res
}

// NewRootCmd initializes the root Cobra command and its persistent flags.
func NewRootCmd() *cobra.Command {
	opts := &GlobalOptions{
		Server:  getEnvOrDefault("RELAY_SERVER_URL", "http://127.0.0.1:8080"),
		APIKey:  os.Getenv("RELAY_API_KEY"),
		Output:  getEnvOrDefault("RELAY_OUTPUT", "table"),
		Timeout: 10 * time.Second,
	}
	if envTimeout := os.Getenv("RELAY_TIMEOUT"); envTimeout != "" {
		if d, err := time.ParseDuration(envTimeout); err == nil {
			opts.Timeout = d
		}
	}

	cmd := &cobra.Command{
		Use:   "relay",
		Short: "Simple Relay — one binary, no infrastructure, reliable HTTP delivery",
		Long: "relay starts the daemon by default. Use subcommands to inspect health, " +
			"monitor queue depth, and triage Dead Letter Queue messages without browser access.",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Default behavior: start the server when no subcommand is provided.
		RunE: runServe,
		PersistentPreRun: func(c *cobra.Command, args []string) {
			c.SetContext(contextWithOptions(c.Context(), opts))
		},
	}

	cmd.PersistentFlags().StringVarP(&opts.Server, "server", "s", opts.Server, "Simple Relay daemon base URL (env: RELAY_SERVER_URL)")
	cmd.PersistentFlags().StringVarP(&opts.APIKey, "api-key", "k", opts.APIKey, "Admin API key for authentication (env: RELAY_API_KEY)")
	cmd.PersistentFlags().StringVarP(&opts.Output, "output", "o", opts.Output, "Output format: table or json (env: RELAY_OUTPUT)")
	cmd.PersistentFlags().DurationVar(&opts.Timeout, "timeout", opts.Timeout, "HTTP client timeout (env: RELAY_TIMEOUT)")

	// Register server daemon flags for default root execution
	var serverOpts ServerOptions
	addServerFlags(cmd, &serverOpts)

	// Register subcommands
	cmd.AddCommand(newServeCmd())
	cmd.AddCommand(newHealthCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newQueueCmd())
	cmd.AddCommand(newDLQCmd())

	return cmd
}

// Execute runs the default root command attached to os.Stdout and os.Stderr.
func Execute() error {
	cmd := NewRootCmd()
	cmd.SetArgs(normalizeArgs(os.Args[1:]))
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
	cmd.SetArgs(normalizeArgs(args))
	cmd.SetOut(out)
	cmd.SetErr(errOut)
	return cmd.Execute()
}
