#!/usr/bin/env bash
set -euo pipefail

# scripts/sync-roadmap.sh
# Synchronizes docs/roadmap.md task checkboxes and snapshot progress percentages
# with GitHub Issues status via gh CLI.

ROADMAP_FILE="docs/roadmap.md"
DRY_RUN=false
MOCK_ISSUES_JSON=""

usage() {
    cat <<EOF
Usage: $0 [OPTIONS]

Options:
    --file <path>        Path to roadmap markdown file (default: docs/roadmap.md)
    --dry-run            Display planned updates without modifying the file
    --mock-json <file>   Use local JSON file instead of querying GitHub API via gh
    -h, --help           Show this help message
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --file)
            ROADMAP_FILE="$2"
            shift 2
            ;;
        --dry-run)
            DRY_RUN=true
            shift
            ;;
        --mock-json)
            MOCK_ISSUES_JSON="$2"
            shift 2
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

if [[ ! -f "$ROADMAP_FILE" ]]; then
    echo "Error: Roadmap file '$ROADMAP_FILE' does not exist." >&2
    exit 1
fi

# Fetch issues from GitHub or mock JSON
ISSUES_JSON=""
if [[ -n "$MOCK_ISSUES_JSON" && -f "$MOCK_ISSUES_JSON" ]]; then
    ISSUES_JSON=$(cat "$MOCK_ISSUES_JSON")
elif command -v gh >/dev/null 2>&1; then
    if gh auth status >/dev/null 2>&1; then
        echo "Fetching issues from GitHub repository..."
        ISSUES_JSON=$(gh issue list --state all --limit 500 --json number,state 2>/dev/null || echo "[]")
    else
        echo "Warning: 'gh' CLI is not authenticated. Skipping remote issue fetch."
        ISSUES_JSON="[]"
    fi
else
    echo "Warning: 'gh' CLI not found. Skipping remote issue fetch."
    ISSUES_JSON="[]"
fi

TEMP_FILE=$(mktemp)
trap 'rm -f "$TEMP_FILE"' EXIT

CHANGES_COUNT=0

# Process line by line and update task status if issue link exists
# Pattern looks for: - [ ] **...** ([#<id>]...) or - [x] **...** ([#<id>]...)
while IFS= read -r line || [[ -n "$line" ]]; do
    UPDATED_LINE="$line"

    if [[ "$line" =~ ^([[:space:]]*-[[:space:]]\[)([[:space:]xX])(\][[:space:]]\*\*[^*]+\*\*.*\[#([0-9]+)\]) ]]; then
        PREFIX="${BASH_REMATCH[1]}"
        CURRENT_MARK="${BASH_REMATCH[2]}"
        REST="${BASH_REMATCH[3]}"
        ISSUE_NUM="${BASH_REMATCH[4]}"

        # Look up state for this issue in ISSUES_JSON
        ISSUE_STATE=$(echo "$ISSUES_JSON" | grep -o "{\"number\":$ISSUE_NUM,\"state\":\"[^\"]*\"}" | grep -o "\"state\":\"[^\"]*\"" | cut -d'"' -f4 || true)

        if [[ "$ISSUE_STATE" == "CLOSED" && "$CURRENT_MARK" != "x" ]]; then
            UPDATED_LINE="${PREFIX}x${REST}"
            CHANGES_COUNT=$((CHANGES_COUNT + 1))
            echo "-> Marked Task (Issue #$ISSUE_NUM) as COMPLETED [x]"
        elif [[ "$ISSUE_STATE" == "OPEN" && "$CURRENT_MARK" == "x" ]]; then
            UPDATED_LINE="${PREFIX} ${REST}"
            CHANGES_COUNT=$((CHANGES_COUNT + 1))
            echo "-> Marked Task (Issue #$ISSUE_NUM) as OPEN [ ]"
        fi
    fi

    echo "$UPDATED_LINE" >> "$TEMP_FILE"
done < "$ROADMAP_FILE"

# Re-calculate Phase completion statistics from updated temp file
calc_phase_progress() {
    local start_pattern="$1"
    local end_pattern="$2"

    local section
    if [[ "$end_pattern" == "$" || -z "$end_pattern" ]]; then
        section=$(sed -n "/$start_pattern/,\$p" "$TEMP_FILE")
    else
        section=$(sed -n "/$start_pattern/,/$end_pattern/p" "$TEMP_FILE")
    fi

    local total
    total=$(echo "$section" | grep -c '^[[:space:]]*-[[:space:]]\[[[:space:]xX]\][[:space:]]\*\*' || true)
    local done_count
    done_count=$(echo "$section" | grep -c '^[[:space:]]*-[[:space:]]\[[xX]\][[:space:]]\*\*' || true)

    if [[ $total -eq 0 ]]; then
        echo "0% (0/0)"
    else
        local pct=$(( (done_count * 100) / total ))
        echo "$pct% ($done_count/$total)"
    fi
}

P1_STAT=$(calc_phase_progress "### Phase 1" "### Phase 2")
P2_STAT=$(calc_phase_progress "### Phase 2" "### Phase 3")
P3_STAT=$(calc_phase_progress "### Phase 3" "### Phase 4")
P4_STAT=$(calc_phase_progress "### Phase 4" "### Phase 5")
P5_STAT=$(calc_phase_progress "### Phase 5" "$")

echo "Phase Progress Summary:"
echo "  Phase 1: $P1_STAT"
echo "  Phase 2: $P2_STAT"
echo "  Phase 3: $P3_STAT"
echo "  Phase 4: $P4_STAT"
echo "  Phase 5: $P5_STAT"

if [[ "$DRY_RUN" == "true" ]]; then
    echo "Dry-run mode: $CHANGES_COUNT task checkbox updates detected. No changes written to $ROADMAP_FILE."
else
    cp "$TEMP_FILE" "$ROADMAP_FILE"
    echo "Successfully updated $ROADMAP_FILE ($CHANGES_COUNT tasks synchronized)."
fi
