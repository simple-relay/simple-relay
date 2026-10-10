#!/usr/bin/env bash
set -euo pipefail

# scripts/migrate-roadmap-to-issues.sh
# Creates GitHub Labels, Milestones, and populates uncompleted roadmap tasks as GitHub Issues.

MODE="dry-run"
UPDATE_ROADMAP=false
ROADMAP_FILE="docs/roadmap.md"

usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Options:
    --dry-run          Print planned actions without executing them (default)
    --apply            Create labels, milestones, and issues using gh CLI
    --update-roadmap   Update docs/roadmap.md with generated issue URLs
    -h, --help         Show this help message
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)
            MODE="dry-run"
            shift
            ;;
        --apply)
            MODE="apply"
            shift
            ;;
        --update-roadmap)
            UPDATE_ROADMAP=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            echo "Unknown option: $1" >&2
            usage
            ;;
    esac
done

echo "Running Roadmap to GitHub Issues Migration Tool (Mode: $MODE)"

# 1. Labels Definition: name|color|description
LABELS=(
    "type:feat|1d76db|New feature or capability"
    "type:bug|d73a4a|Bug report or defect"
    "type:docs|0075ca|Documentation update or specification"
    "type:refactor|bfdadc|Code refactoring without behavior change"
    "type:chore|c5def5|Build scripts, dependencies, or repo maintenance"
    "area:engine|5319e7|Core execution engine (batch writer, retry, worker pool)"
    "area:storage|0e8a16|SQLite repository, schema migrations, WAL handling"
    "area:api|fbca04|HTTP handlers, routing, middlewares"
    "area:security|b60205|HMAC verification, CSRF, admin auth"
    "area:ui|e99695|Embedded Web UI (Alpine.js, Tailwind)"
    "area:ci|128A0C|GitHub Actions, release pipeline, GoReleaser"
    "status:triage|d4c5f9|Newly opened issue awaiting review"
    "status:ready|0e8a16|Refined and ready to be picked up"
    "status:in-progress|fbca04|Actively being worked on by an assignee"
    "status:blocked|e11d48|Blocked by dependencies or open design questions"
    "good first issue|7057ff|Good for newcomers"
    "help wanted|008672|Extra attention is needed"
)

# 2. Milestones: Deprecated (project uses Phase-only structure)
MILESTONES=()

# 3. Tasks Definition: task_id|title|milestone|labels|body
TASKS=(
    "Task 2.1.2|Task 2.1.2: Signature Verification Adapters (internal/signature)|v0.2.0 - Inbound Webhooks & Security Pipeline|type:feat,area:security,status:ready|### What needs to be done?
- Implement Verifier interface: \`Verify(header string, secret string, rawBody []byte) error\`
- Implement Stripe HMAC adapter: parse \`t=timestamp,v1=signature\`, verify HMAC-SHA256 with timestamp tolerance.
- Implement GitHub HMAC adapter: verify \`sha256=<hex>\` against \`X-Hub-Signature-256\`.
- Implement Generic HMAC-SHA256 adapter.

### Why & Context
Complete Phase 2 security ingestion layer. Reference: docs/architecture.md and docs/roadmap.md."

    "Task 3.1.1|Task 3.1.1: Embedded Assets & Base Layout (ui/)|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:ui,status:ready|### What needs to be done?
- Embed HTML templates and static assets using \`go:embed\`.
- Zero Node.js build pipeline: Alpine.js + Tailwind CSS.

### Why & Context
Foundational UI layout for Phase 3 embedded dashboard."

    "Task 3.1.2|Task 3.1.2: Routes Management UI|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:ui,status:ready|### What needs to be done?
- View all active routes (inbound & outbound).
- Create, edit, and delete routes via UI modal.
- Verify instant hot-reloading in UI.

### Why & Context
Web-based management of routes."

    "Task 3.1.3|Task 3.1.3: Requests & Attempts Explorer|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:ui,status:ready|### What needs to be done?
- Filterable requests table: search by ID, status (\`pending\`, \`success\`, \`dlq\`), route name, date range.
- Slide-over detail drawer: inspect request headers, raw body, attempt status codes, duration, and error logs.

### Why & Context
Visual observability into request lifecycle and attempts."

    "Task 3.1.4|Task 3.1.4: DLQ Dashboard & Replay Action|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:ui,status:ready|### What needs to be done?
- Dedicated view for failed/DLQ events.
- One-click individual replay and bulk replay buttons.

### Why & Context
DLQ inspection and recovery interface."

    "Task 3.2.1|Task 3.2.1: Admin Password Authentication & CSRF Protection|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:security,status:ready|### What needs to be done?
- Protect UI routes with session cookie authenticated against \`RELAY_ADMIN_PASSWORD\`.
- CSRF token validation on all state-mutating requests (Route CRUD, Replay, Purge).
- Login rate limiter to prevent brute-force attacks.

### Why & Context
Security boundary for admin operations and embedded UI."

    "Task 3.2.2|Task 3.2.2: Storage Retention Cleaner (internal/engine/cleaner.go)|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:storage,status:ready|### What needs to be done?
- Periodic background cleaner worker (hourly/daily).
- Purge successful requests older than \`RELAY_RETENTION_SUCCESS_DAYS\` (default 7 days).
- Purge DLQ requests older than \`RELAY_RETENTION_FAILED_DAYS\` (default 30 days).
- Execute \`PRAGMA incremental_vacuum(1000)\` to reclaim disk pages.

### Why & Context
Prevent database bloat over extended runtimes."

    "Task 3.2.3|Task 3.2.3: Litestream Disaster Recovery Integration|v0.3.0 - Embedded Web UI & System Lifecycle|type:docs,area:storage,status:ready|### What needs to be done?
- Provide production configuration guide and Docker compose setup for Litestream S3 streaming replication.
- Verify zero-SPOF SQLite durability.

### Why & Context
High availability and disaster recovery for SQLite."

    "Task 3.2.4|Task 3.2.4: Versioned Database Schema Migrations Engine|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:storage,status:ready|### What needs to be done?
- Embed migration SQL files directly with \`go:embed\`.
- Maintain durable version history via \`schema_migrations\` table.
- Sequentially execute pending migrations on boot within \`BEGIN IMMEDIATE\` transactions.
- Automatic baseline migration for existing databases.

### Why & Context
Reliable database schema evolution. Reference: docs/specs/2026-10-09-database-migrations-engine-design.md."

    "Task 3.3.1|Task 3.3.1: Multi-Platform Build Configuration (.goreleaser.yaml)|v0.3.0 - Embedded Web UI & System Lifecycle|type:chore,area:ci,status:ready|### What needs to be done?
- Zero-CGO static compilation (\`CGO_ENABLED=0\`) across Linux, macOS, and Windows.
- Automated archive packaging (\`.tar.gz\`, \`.zip\`) with SHA-256 checksum generation.
- Multi-architecture Docker image build and push configuration.

### Why & Context
Cross-platform distribution without manual builds."

    "Task 3.3.2|Task 3.3.2: Automated Versioning & Release Workflow (.github/workflows/release.yml)|v0.3.0 - Embedded Web UI & System Lifecycle|type:chore,area:ci,status:ready|### What needs to be done?
- Integrate \`release-please\` for Conventional Commits and automated \`CHANGELOG.md\`.
- Automatically manage SemVer release pull requests on \`main\`.
- Trigger GoReleaser upon Release PR merge.

### Why & Context
Automated release management."

    "Task 3.3.3|Task 3.3.3: In-App Version Update Checker (internal/version)|v0.3.0 - Embedded Web UI & System Lifecycle|type:feat,area:engine,status:ready|### What needs to be done?
- Background non-blocking version checker querying upstream GitHub Releases.
- Dual discovery: GitHub REST API with fallback to HTTP HEAD redirect trick.
- SemVer comparison using \`golang.org/x/mod/semver\`.
- Expose via \`GET /api/v1/system/version\` and Web UI notification badge.

### Why & Context
Self-hosted update notification. Reference: docs/specs/2026-10-09-version-check-design.md."

    "Task 0.3.1|Task 0.3.1: CLI inspection binary (cmd/relay-cli)|v0.4.0 - Utility & Observability Extensions|type:feat,area:api,status:ready|### What needs to be done?
- CLI tool (\`cmd/relay-cli\`) for inspecting relay health, routes, and queue depth.

### Why & Context
Terminal-based operational troubleshooting."

    "Task 0.3.2|Task 0.3.2: Webhook alert notifications on DLQ threshold breaches|v0.4.0 - Utility & Observability Extensions|type:feat,area:engine,status:ready|### What needs to be done?
- Webhook alert notifications (Slack / Discord / Email) when DLQ count exceeds configurable threshold.

### Why & Context
Proactive failure alerting."

    "Task 0.3.3|Task 0.3.3: API Key authentication for programmatic management|v0.4.0 - Utility & Observability Extensions|type:feat,area:security,status:ready|### What needs to be done?
- API Key authentication for programmatic API management.

### Why & Context
Secure programmatic access for external orchestration."

    "Task 0.3.4|Task 0.3.4: Prometheus metrics exporter endpoint (/metrics)|v0.4.0 - Utility & Observability Extensions|type:feat,area:api,status:ready|### What needs to be done?
- Prometheus metrics exporter endpoint (\`GET /metrics\`) tracking ingestion rate, retry counts, queue depth, and latency histograms.

### Why & Context
Enterprise metrics scraping."

    "Task 1.0.1|Task 1.0.1: Pluggable PostgreSQL storage adapter|v1.0.0 - Enterprise Edition|type:feat,area:storage,status:ready|### What needs to be done?
- Pluggable PostgreSQL storage adapter (\`internal/storage/postgres\`) implementing \`storage.Storage\`.

### Why & Context
Enterprise scalable persistence."

    "Task 1.0.2|Task 1.0.2: Multi-node worker clustering with leader election|v1.0.0 - Enterprise Edition|type:feat,area:engine,status:ready|### What needs to be done?
- Multi-node worker clustering with distributed leader election / advisory locks.

### Why & Context
High throughput horizontal scaling."

    "Task 1.0.3|Task 1.0.3: Enterprise SSO integration (OIDC, OAuth2, SAML)|v1.0.0 - Enterprise Edition|type:feat,area:security,status:ready|### What needs to be done?
- Enterprise SSO integration (OIDC, OAuth2, SAML).

### Why & Context
Enterprise identity federation."

    "Task 1.0.4|Task 1.0.4: Role-Based Access Control (RBAC) and audit logs|v1.0.0 - Enterprise Edition|type:feat,area:security,status:ready|### What needs to be done?
- Role-Based Access Control (RBAC) and security audit logs.

### Why & Context
Multi-tenant security compliance."
)

echo "Total Labels:     ${#LABELS[@]}"
echo "Total Milestones: ${#MILESTONES[@]}"
echo "Total Issues:     ${#TASKS[@]}"
echo ""

if [[ "$MODE" == "dry-run" ]]; then
    echo "=== [DRY RUN] Milestones ==="
    for item in "${MILESTONES[@]}"; do
        IFS="|" read -r title desc <<< "$item"
        echo "  [Milestone] $title -> $desc"
    done
    echo ""

    echo "=== [DRY RUN] Labels ==="
    for item in "${LABELS[@]}"; do
        IFS="|" read -r name color desc <<< "$item"
        echo "  [Label] $name (#$color) -> $desc"
    done
    echo ""

    echo "=== [DRY RUN] Issues to Create ==="
    for item in "${TASKS[@]}"; do
        IFS="|" read -r task_id title milestone labels body <<< "$item"
        echo "  [Issue] $title | Milestone: '$milestone' | Labels: [$labels]"
    done
    echo ""
    echo "Dry run completed successfully. Use --apply to execute remotely."
    exit 0
fi

# Apply Mode
echo "Applying changes via gh CLI..."
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null || echo "simple-relay/simple-relay")

for item in "${LABELS[@]}"; do
    IFS="|" read -r name color desc <<< "$item"
    echo "Creating/updating label: $name"
    gh label create "$name" --color "$color" --description "$desc" --force 2>/dev/null || true
done

for item in "${MILESTONES[@]}"; do
    IFS="|" read -r title desc <<< "$item"
    echo "Creating milestone: $title"
    gh api "repos/${REPO}/milestones" -f title="$title" -f description="$desc" 2>/dev/null || true
done

for item in "${TASKS[@]}"; do
    IFS="|" read -r task_id title milestone labels body <<< "$item"
    echo "Creating issue: $title"
    LABEL_ARGS=()
    IFS=',' read -ra SPLIT_LABELS <<< "$labels"
    for lbl in "${SPLIT_LABELS[@]}"; do
        LABEL_ARGS+=(--label "$lbl")
    done
    ISSUE_URL=$(gh issue create --title "$title" "${LABEL_ARGS[@]}" --body "$body" 2>/dev/null || echo "")
    echo "Created: $ISSUE_URL"
    
    if [[ "$UPDATE_ROADMAP" == "true" && -n "$ISSUE_URL" ]]; then
        ISSUE_NUM="${ISSUE_URL##*/}"
        # Inject link into roadmap file after the bold task title
        sed -i "s|\(\*\*${task_id}:[^*]*\*\*\)|\1 ([#${ISSUE_NUM}](${ISSUE_URL}))|" "$ROADMAP_FILE"
    fi
done

echo "Migration finished successfully!"
