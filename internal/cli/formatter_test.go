package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestFormatStatus(t *testing.T) {
	t.Run("terminal with colors", func(t *testing.T) {
		green := FormatStatus("ok", true)
		if !strings.Contains(green, "\033[32m") || !strings.Contains(green, "ok") {
			t.Fatalf("expected green ANSI code for ok status, got %q", green)
		}

		healthy := FormatStatus("healthy", true)
		if !strings.Contains(healthy, "\033[32m") {
			t.Fatalf("expected green ANSI code for healthy status, got %q", healthy)
		}

		pending := FormatStatus("pending", true)
		if !strings.Contains(pending, "\033[33m") {
			t.Fatalf("expected yellow ANSI code for pending status, got %q", pending)
		}

		failed := FormatStatus("failed", true)
		if !strings.Contains(failed, "\033[31m") {
			t.Fatalf("expected red ANSI code for failed status, got %q", failed)
		}

		backlog := FormatStatus("backlog", true)
		if !strings.Contains(backlog, "\033[31m") {
			t.Fatalf("expected red ANSI code for backlog status, got %q", backlog)
		}
	})

	t.Run("non-terminal without colors", func(t *testing.T) {
		plain := FormatStatus("ok", false)
		if strings.Contains(plain, "\033[") {
			t.Fatalf("expected no ANSI escape codes when isTerminal is false, got %q", plain)
		}
		if plain != "ok" {
			t.Fatalf("expected exact status 'ok', got %q", plain)
		}
	})
}

func TestTruncate(t *testing.T) {
	if got := Truncate("short", 10); got != "short" {
		t.Fatalf("expected 'short', got %q", got)
	}

	if got := Truncate("01ja2b3c4d5e6f7g8h9j0k1m2n", 10); got != "01ja2b3c4d..." {
		t.Fatalf("expected truncated string with ellipsis, got %q", got)
	}

	// Test multi-byte UTF-8 string truncation
	unicodeStr := "xin chào việt nam"
	if got := Truncate(unicodeStr, 8); got != "xin chào..." {
		t.Fatalf("expected rune-safe truncated string, got %q", got)
	}

	if got := Truncate("hello", 0); got != "" {
		t.Fatalf("expected empty string for maxLen <= 0, got %q", got)
	}
	if got := Truncate("hello", -1); got != "" {
		t.Fatalf("expected empty string for negative maxLen, got %q", got)
	}
}

func TestIsTerminal(t *testing.T) {
	buf := &bytes.Buffer{}
	if IsTerminal(buf) {
		t.Fatal("expected IsTerminal false for bytes.Buffer")
	}
}

func TestFormatStatus_EdgeCases(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	if got := FormatStatus("ok", true); got != "ok" {
		t.Fatalf("expected plain status with NO_COLOR, got %q", got)
	}

	t.Setenv("NO_COLOR", "")
	if got := FormatStatus("custom_unknown_status", true); got != "custom_unknown_status" {
		t.Fatalf("expected plain status for unknown status, got %q", got)
	}
}

func TestSanitizeTerminal(t *testing.T) {
	t.Run("strips ANSI color escapes", func(t *testing.T) {
		input := "\x1b[31m502 Bad Gateway\x1b[0m"
		expected := "502 Bad Gateway"
		if got := SanitizeTerminal(input); got != expected {
			t.Fatalf("expected %q, got %q", expected, got)
		}
	})

	t.Run("strips OSC window title injection", func(t *testing.T) {
		input := "\x1b]0;Pwned Terminal\x07504 Gateway Timeout"
		expected := "504 Gateway Timeout"
		if got := SanitizeTerminal(input); got != expected {
			t.Fatalf("expected %q, got %q", expected, got)
		}
	})

	t.Run("replaces control characters with spaces", func(t *testing.T) {
		input := "Connection\r\nrefused:\tport 8080\x00"
		expected := "Connection  refused: port 8080"
		if got := SanitizeTerminal(input); got != expected {
			t.Fatalf("expected %q, got %q", expected, got)
		}
	})

	t.Run("preserves valid unicode text", func(t *testing.T) {
		input := "Lỗi kết nối máy chủ upstream"
		if got := SanitizeTerminal(input); got != input {
			t.Fatalf("expected %q, got %q", input, got)
		}
	})
}

func TestRenderTable(t *testing.T) {
	var buf bytes.Buffer
	headers := []string{"ID", "STATUS", "LATENCY"}
	rows := [][]string{
		{"req_1", "ok", "1.2ms"},
		{"req_2", "failed", "15ms"},
	}

	if err := RenderTable(&buf, headers, rows); err != nil {
		t.Fatalf("unexpected error rendering table: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ID") || !strings.Contains(out, "STATUS") {
		t.Fatalf("expected headers in output, got:\n%s", out)
	}
	if !strings.Contains(out, "req_1") || !strings.Contains(out, "req_2") {
		t.Fatalf("expected rows in output, got:\n%s", out)
	}
}

func TestRenderJSON(t *testing.T) {
	var buf bytes.Buffer
	data := map[string]any{
		"status": "ok",
		"total":  42,
	}

	if err := RenderJSON(&buf, data); err != nil {
		t.Fatalf("unexpected error rendering json: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, `"status": "ok"`) || !strings.Contains(out, `"total": 42`) {
		t.Fatalf("expected indented JSON output, got:\n%s", out)
	}
}
