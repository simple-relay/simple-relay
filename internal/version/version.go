package version

// Injected at build time via -ldflags.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// UserAgent returns the standard User-Agent header string.
func UserAgent() string {
	return "simple-relay/" + Version
}
