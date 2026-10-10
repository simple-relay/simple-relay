#!/usr/bin/env bash
# ==============================================================================
# Simple Relay — End-to-End Performance Benchmark Runner
# Measures: Ingestion RPS, Latency Percentiles (p50, p95, p99), Lock Contention,
#           and Upstream Dispatcher Drain Rate.
# ==============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Configuration
RELAY_PORT="${RELAY_PORT:-8181}"
MOCK_PORT="${MOCK_PORT:-9191}"
NUM_REQUESTS="${NUM_REQUESTS:-2000}"
CONCURRENCY_LIST="${CONCURRENCY_LIST:-"10 50 100"}"
RUN_MODE="${RUN_MODE:-docker}" # "docker" or "bare-metal"
DOCKER_IMAGE="${DOCKER_IMAGE:-simple-relay:v0.3.0}"

BOMBARDIER="${BOMBARDIER:-$(command -v bombardier || echo "$HOME/go/bin/bombardier")}"
if [[ ! -x "$BOMBARDIER" ]]; then
  echo "Error: bombardier binary not found. Install via: go install github.com/codesenberg/bombardier@latest" >&2
  exit 1
fi

TEMP_DIR="$(mktemp -d /tmp/relay-bench-XXXXXX)"
chmod 777 "$TEMP_DIR"
DB_FILE="$TEMP_DIR/bench.db"
PAYLOAD_FILE="$TEMP_DIR/payload.json"

MOCK_PID=""
CONTAINER_NAME="relay-bench-$(date +%s)"
RELAY_PID=""

cleanup() {
  echo ""
  echo "Cleaning up benchmark processes..."
  if [[ -n "$MOCK_PID" ]]; then
    kill "$MOCK_PID" 2>/dev/null || true
  fi
  if [[ "$RUN_MODE" == "docker" ]]; then
    docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
  else
    if [[ -n "$RELAY_PID" ]]; then
      kill "$RELAY_PID" 2>/dev/null || true
    fi
  fi
  rm -rf "$TEMP_DIR" 2>/dev/null || true
  echo "Cleanup complete."
}
trap cleanup EXIT INT TERM

# Prepare sample webhook payload (~1.5 KB)
cat > "$PAYLOAD_FILE" <<'JSON'
{
  "event_id": "evt_bench_9823471982734",
  "event_type": "invoice.payment_succeeded",
  "created_at": 1728571200,
  "data": {
    "object": {
      "id": "in_1Px98723948729384",
      "customer": "cus_9872938472934",
      "amount_due": 990000,
      "amount_paid": 990000,
      "currency": "vnd",
      "lines": [
        {"item": "enterprise_plan_annual", "quantity": 1, "unit_amount": 900000},
        {"item": "vat_tax_10_percent", "quantity": 1, "unit_amount": 90000}
      ],
      "billing_details": {
        "email": "customer@example.vn",
        "name": "Nguyen Van A",
        "phone": "+84901234567",
        "address": {"city": "Ho Chi Minh City", "country": "VN", "line1": "123 Nguyen Hue"}
      },
      "metadata": {
        "environment": "production",
        "source": "payment_gateway",
        "trace_id": "trace-98723948723-abc"
      }
    }
  }
}
JSON

echo "================================================================================"
echo "⚡ SIMPLE RELAY — PERFORMANCE BENCHMARK SUITE"
echo "================================================================================"
echo " Mode:              $RUN_MODE ($([ "$RUN_MODE" == "docker" ] && echo "$DOCKER_IMAGE" || echo "binary"))"
echo " Relay Address:     http://127.0.0.1:$RELAY_PORT"
echo " Upstream Mock:     http://127.0.0.1:$MOCK_PORT"
echo " Requests/Tier:     $NUM_REQUESTS"
echo " Concurrency Tiers: $CONCURRENCY_LIST"
echo " Temp DB:           $DB_FILE"
echo "================================================================================"

# 1. Start Mock Upstream Server
echo "[1/4] Starting Mock Upstream Server on port $MOCK_PORT..."
go run "$REPO_ROOT/scripts/mock_upstream.go" -port "$MOCK_PORT" > "$TEMP_DIR/mock.log" 2>&1 &
MOCK_PID=$!

for i in {1..30}; do
  if curl -s "http://127.0.0.1:$MOCK_PORT/healthz" | grep -q "ok"; then
    break
  fi
  sleep 0.1
done

# 2. Start Simple Relay
echo "[2/4] Starting Simple Relay ($RUN_MODE)..."
if [[ "$RUN_MODE" == "docker" ]]; then
  docker run --rm -d \
    --name "$CONTAINER_NAME" \
    --network host \
    -v "$TEMP_DIR:/data" \
    "$DOCKER_IMAGE" \
    -port "$RELAY_PORT" \
    -db "/data/bench.db" \
    -log-level warn > "$TEMP_DIR/docker.log" 2>&1
else
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "$TEMP_DIR/relay" ./cmd/relay)
  "$TEMP_DIR/relay" -port "$RELAY_PORT" -db "$DB_FILE" -log-level warn > "$TEMP_DIR/relay.log" 2>&1 &
  RELAY_PID=$!
fi

for i in {1..50}; do
  if curl -s "http://127.0.0.1:$RELAY_PORT/healthz" | grep -q "status"; then
    break
  fi
  sleep 0.1
done

if ! curl -s "http://127.0.0.1:$RELAY_PORT/healthz" | grep -q "status"; then
  echo "Error: Simple Relay failed to start within 5s." >&2
  exit 1
fi
echo "✓ Simple Relay is healthy and ready."

# 3. Create Benchmark Route
echo "[3/4] Registering test route 'bench-route'..."
ROUTE_RESP="$(curl -s -X POST "http://127.0.0.1:$RELAY_PORT/api/v1/routes" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"bench-route\",
    \"path\": \"/api/v1/webhook/bench-route\",
    \"destination_url\": \"http://127.0.0.1:$MOCK_PORT/webhook\",
    \"type\": \"inbound\",
    \"mode\": \"async\",
    \"max_attempts\": 3,
    \"timeout_ms\": 2000,
    \"retry_backoff_strategy\": \"exp\"
  }")"

if ! grep -q '"name":"bench-route"' <<< "$ROUTE_RESP"; then
  echo "Error creating route: $ROUTE_RESP" >&2
  exit 1
fi
echo "✓ Route registered successfully."

# Warmup run
echo "[4/4] Warming up connection pools (500 reqs)..."
"$BOMBARDIER" -c 10 -n 500 -q -m POST \
  -H "Content-Type: application/json" \
  -f "$PAYLOAD_FILE" \
  "http://127.0.0.1:$RELAY_PORT/api/v1/webhook/bench-route" >/dev/null 2>&1

# Wait for warmup drain
for i in {1..20}; do
  RC=$(curl -s "http://127.0.0.1:$MOCK_PORT/stats" | jq -r '.received_count')
  if [[ "$RC" -ge 500 ]]; then break; fi
  sleep 0.2
done
curl -s -X POST "http://127.0.0.1:$MOCK_PORT/reset" >/dev/null 2>&1

echo ""
echo "🚀 EXECUTING LOAD TESTING MATRIX..."
echo ""

# Results storage
RESULTS=()

for C in $CONCURRENCY_LIST; do
  echo ">>> [Tier C=$C] Ingesting $NUM_REQUESTS requests..."
  curl -s -X POST "http://127.0.0.1:$MOCK_PORT/reset" >/dev/null 2>&1

  START_TIME=$(date +%s%N)
  OUTPUT="$("$BOMBARDIER" -c "$C" -n "$NUM_REQUESTS" -l -o json -p r -m POST \
    -H "Content-Type: application/json" \
    -f "$PAYLOAD_FILE" \
    "http://127.0.0.1:$RELAY_PORT/api/v1/webhook/bench-route")"

  RPS="$(jq -r '.result.rps.mean | . * 100 | round / 100' <<< "$OUTPUT")"
  AVG_LAT="$(jq -r '(.result.latency.mean / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  P50_LAT="$(jq -r '(.result.latency.percentiles["50"] / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  P90_LAT="$(jq -r '(.result.latency.percentiles["90"] / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  P95_LAT="$(jq -r '(.result.latency.percentiles["95"] / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  P99_LAT="$(jq -r '(.result.latency.percentiles["99"] / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  MAX_LAT="$(jq -r '(.result.latency.max / 1000) | . * 100 | round / 100' <<< "$OUTPUT")"
  REQ_2XX="$(jq -r '.result.req2xx' <<< "$OUTPUT")"
  REQ_ERR="$(jq -r '.result.req5xx + .result.req4xx' <<< "$OUTPUT")"

  echo "    ✓ Ingest Phase Done: $REQ_2XX requests committed | RPS: $RPS | p50: ${P50_LAT}ms | p99: ${P99_LAT}ms"

  # Wait for background dispatcher to drain
  echo -n "    ⏳ Draining queue to upstream mock: "
  START_DRAIN=$(date +%s)
  DELIVERED=0
  DRAIN_TIME=0
  MAX_WAIT=60
  while true; do
    STATS="$(curl -s "http://127.0.0.1:$MOCK_PORT/stats")"
    DELIVERED="$(jq -r '.received_count' <<< "$STATS")"
    NOW=$(date +%s)
    DRAIN_TIME=$((NOW - START_DRAIN))

    if [[ "$DELIVERED" -ge "$NUM_REQUESTS" ]]; then
      echo "Complete! ($DELIVERED/$NUM_REQUESTS delivered in ${DRAIN_TIME}s)"
      break
    fi

    if [[ "$DRAIN_TIME" -ge "$MAX_WAIT" ]]; then
      echo "Timeout ($DELIVERED/$NUM_REQUESTS delivered in ${DRAIN_TIME}s)"
      break
    fi

    echo -n "."
    sleep 1
  done

  DRAIN_RPS="N/A"
  if [[ "$DRAIN_TIME" -gt 0 ]]; then
    DRAIN_RPS=$(awk "BEGIN {printf \"%.1f\", $DELIVERED / $DRAIN_TIME}")
  fi

  echo "    ✓ Dispatch Rate: ~${DRAIN_RPS} events/s"
  echo ""

  RESULTS+=("$C|$RPS|$AVG_LAT|$P50_LAT|$P90_LAT|$P95_LAT|$P99_LAT|$MAX_LAT|$REQ_2XX|$REQ_ERR|$DELIVERED|$DRAIN_RPS")
done

# Check database file sizes
DB_SIZE="$(du -h "$DB_FILE" 2>/dev/null | awk '{print $1}' || echo "N/A")"
WAL_SIZE="$(du -h "$DB_FILE-wal" 2>/dev/null | awk '{print $1}' || echo "0K")"

echo "================================================================================"
echo "📊 BENCHMARK RESULTS SUMMARY (Docker v0.3.0)"
echo "================================================================================"
echo ""
echo "| Concurrency | Ingest RPS | Avg (ms) | p50 (ms) | p90 (ms) | p95 (ms) | p99 (ms) | Max (ms) | Ingested | Delivered | Dispatch Rate |"
echo "|:-----------:|:----------:|:--------:|:--------:|:--------:|:--------:|:--------:|:--------:|:--------:|:---------:|:-------------:|"

for row in "${RESULTS[@]}"; do
  IFS="|" read -r c rps avg p50 p90 p95 p99 max succ err disp drps <<< "$row"
  printf "| %-11s | %-10s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-8s | %-9s | %-13s |\n" \
    "$c" "$rps" "$avg" "$p50" "$p90" "$p95" "$p99" "$max" "$succ" "$disp" "${drps} ev/s"
done

echo ""
echo "Database WAL Storage:"
echo " - DB File Size:  $DB_SIZE"
echo " - WAL File Size: $WAL_SIZE"
echo "================================================================================"
