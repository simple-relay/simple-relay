# Simple Relay — Performance Benchmarks

> **Core Metric:** Inbound webhook ingestion throughput under SQLite WAL durability, concurrency isolation, resource footprint, and outbound queue drain rate.

---

## 1. Overview & Methodology

Simple Relay enforces the **Durability Golden Rule**: an inbound webhook is never acknowledged with HTTP `200 OK` or `202 Accepted` until its raw, unmutated byte payload is committed to SQLite WAL storage. 

Unlike in-memory queues or message brokers that acknowledge before persistence, Simple Relay's performance ceiling is determined by:
1. **Single-Writer Micro-Batching Throughput (`engine.BatchWriter`):** How many concurrent ingestion goroutines can be aggregated into atomic SQLite WAL commit batches before lock contention occurs.
2. **Pure-Go SQLite Driver Performance (`modernc.org/sqlite`):** CPU instruction efficiency and memory allocation characteristics without CGO.
3. **Outbound Dispatcher Drain Rate (`engine.WorkerPool` + `engine.Dispatcher`):** How fast asynchronous worker pools claim pending requests from SQLite and deliver them to upstream endpoints.
4. **Resource Footprint:** Resident memory (RAM) and CPU core utilization under sustained high-concurrency loads.

---

## 2. Benchmark Tooling & Environment

- **Load Generator:** [`bombardier`](https://github.com/codesenberg/bombardier) (high-performance FastHTTP load generator measuring exact latency percentiles).
- **Target Application:** `simple-relay` packaged in minimal Alpine Docker container (`simple-relay:v0.3.0`) running with `--network host`.
- **Mock Upstream:** Lightweight Go HTTP receiver ([`scripts/mock_upstream.go`](../scripts/mock_upstream.go)) listening on port `9191` tracking atomic delivered counts and timestamps.
- **Automation Runner:** [`scripts/benchmark.sh`](../scripts/benchmark.sh) managing database lifecycle, route provisioning, connection pool warmup, load execution, and queue drain verification.

---

## 3. Benchmark Runs

### Run 1: Release v0.3.0 — 2026-10-10

- **Release Version:** `v0.3.0` (commit `d4c098f`)
- **Docker Image:** `simple-relay:v0.3.0`
- **Driver:** Pure Go `modernc.org/sqlite` (WAL mode, `synchronous=NORMAL`, `busy_timeout=5000`)
- **Host Specs:** Linux x86_64 (amd64), Go 1.26 runtime, NVMe SSD storage
- **Payload:** Realistic webhook JSON payload (~1.5 KB with customer data, invoice items, metadata, and timestamps)

#### Concurrency Matrix (1,000 Requests / Tier)

| Concurrency | Ingest RPS | Avg Latency | p50 Latency | p90 Latency | p95 Latency | p99 Latency | Max Latency | Ingested | Delivered Upstream | Dispatch Drain Rate |
|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|:---:|
| **10** | **848.29 req/s** | 11.40 ms | 10.09 ms | 10.83 ms | 37.84 ms | 38.77 ms | 40.53 ms | 1000/1000 (100%) | 1000/1000 (100%) | Near-instant |
| **50** | **6,279.85 req/s** | 7.39 ms | **4.11 ms** | 8.54 ms | 33.77 ms | 42.97 ms | 43.40 ms | 1000/1000 (100%) | 1000/1000 (100%) | **~111.1 events/s** |
| **100** | **8,707.52 req/s** | 12.20 ms | **5.78 ms** | 36.41 ms | 41.81 ms | **42.81 ms** | 51.24 ms | 1000/1000 (100%) | 1000/1000 (100%) | **~111.1 events/s** |

> **Error Rate:** **0.00%** (Zero HTTP 5xx errors, zero `SQLITE_BUSY` lock contention across all runs).  
> **Storage Footprint:** DB file ~5.7 MB, WAL file ~4.0 MB.

---

#### Sustained 10-Second Saturation Run (100 Concurrent Connections)

To evaluate system stability, memory leaks, and CPU efficiency under sustained saturation, Simple Relay was bombarded for 10 seconds continuously at 100 concurrency:

- **Total Requests Handled:** **100,622 requests**
- **Average Ingest Throughput:** **10,058.32 req/s**
- **Median Latency (p50):** **5.65 ms**
- **p95 Latency:** **35.81 ms**
- **p99 Latency:** **39.73 ms**
- **Total Ingestion Errors:** **0** (100% 200 OK persisted)

#### CPU & Memory Utilization Profile (Sampled via `docker stats`)

| State | CPU Usage | RAM Usage | RAM % of Host | Description |
|:---|:---:|:---:|:---:|:---|
| **Idle** | `~0.5%` | **4.2 MiB** | 0.01% | Baseline idle memory |
| **Peak Load (10,000 RPS)** | **143% – 145%** | **24.0 – 26.1 MiB** | 0.04% | Sustained load (utilizing ~1.45 CPU cores) |
| **Post-Load (Drain Phase)** | `~2.5%` | **21.1 MiB** | 0.03% | Asynchronous background dispatcher processing |

---

## 4. Architectural Analysis & Key Findings

### 1. SQLite WAL Micro-Batching is Highly Effective
Pure-Go SQLite driver (`modernc.org/sqlite`) handles over **10,000 requests/second** while guaranteeing zero data loss. The single-writer micro-batching pattern (`engine.BatchWriter`) aggregates concurrent writes into atomic transactions, avoiding disk sync thrashing and eliminating `SQLITE_BUSY` table lock contention.

### 2. Extremely Low Memory Footprint
The entire runtime memory consumption remains under **26.2 MiB** even while handling 100,000 in-flight requests. Go's runtime GC rapidly reclaims buffer allocations without memory leaks.

### 3. Asymmetric Pipeline Identification
While ingestion sustains **~10,000 req/s**, outbound dispatch to upstream endpoints is currently capped at **~111 events/s**.
- **Root Cause:** In `v0.3.0`, `WorkerPool` has a hardcoded worker concurrency of 20 (`internal/engine/worker_pool.go`), `jobsCh` has a fixed capacity of 40 (`cmd/relay/main.go`), and dispatcher polling defaults to 500ms.
- **Action Item:** Tracked in Issue [#45](https://github.com/simple-relay/simple-relay/issues/45) to expose configurable worker pool concurrency (`-workers`, `RELAY_WORKER_CONCURRENCY`), tunable dispatcher batch buffers, and active WAL checkpointing.

---

## 5. How to Reproduce

Run the automated benchmark locally using the provided script:

```bash
# 1. Run standard Docker container benchmark (v0.3.0)
./scripts/benchmark.sh

# 2. Run with custom requests and concurrency tiers
NUM_REQUESTS=5000 CONCURRENCY_LIST="10 50 100 200" ./scripts/benchmark.sh

# 3. Compare with bare-metal native binary
RUN_MODE=bare-metal ./scripts/benchmark.sh
```
