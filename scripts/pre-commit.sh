#!/usr/bin/env bash
# Simple Relay — Git Pre-Commit Hook
# Validates code formatting, module tidiness, static analysis, and compilation before committing.

set -euo pipefail

# Ensure Go compiler is in PATH if installed locally
if [ -d "$HOME/.local/go/bin" ]; then
    export PATH="$HOME/.local/go/bin:$PATH"
fi

echo "🔍 [pre-commit] Checking Go formatting (gofmt -s)..."
UNFORMATTED=$(gofmt -s -l .)
if [ -n "$UNFORMATTED" ]; then
    echo "❌ [pre-commit] Unformatted files found:"
    echo "$UNFORMATTED"
    echo "👉 Fix by running: make fmt (or 'gofmt -s -w .')"
    exit 1
fi

echo "🔍 [pre-commit] Checking module tidiness (go mod tidy)..."
go mod tidy
if ! git diff --exit-code go.mod go.sum >/dev/null 2>&1; then
    echo "❌ [pre-commit] go.mod or go.sum has uncommitted changes after 'go mod tidy'."
    echo "👉 Fix by running: make tidy && git add go.mod go.sum"
    exit 1
fi

echo "🔍 [pre-commit] Running static analysis (go vet)..."
go vet ./...

echo "🔍 [pre-commit] Verifying static compilation (CGO_ENABLED=0)..."
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dev/null ./cmd/relay

echo "✅ [pre-commit] All pre-commit quality checks passed!"
