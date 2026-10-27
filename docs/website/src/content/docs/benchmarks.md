---
title: "Benchmarks & Performance"
description: "Sustained ingestion throughput, latency percentiles, and memory footprint measured under realistic loads."
section: "Reference"
order: 13
---

Simple Relay is engineered in pure Go to deliver maximum reliability with a near-zero memory footprint.

Unlike in-memory queues that acknowledge receipt before committing to disk, Simple Relay enforces the **Durability Golden Rule**: an inbound webhook is never acknowledged with HTTP `200 OK` until its raw byte payload is safely flushed to persistent storage.

Below are the official benchmark measurements conducted against Release `v0.3.0`.

These are Simple Relay measurements, not a head-to-head comparison with another webhook product. They show request acknowledgement and durable ingestion performance under the listed test conditions. Results depend on hardware, storage, network, configuration, and workload. Use the reproduction steps below to run the same workload in your own environment before comparing systems.

---

## 1. Concurrency & Ingestion Throughput

Tests were executed using [`bombardier`](https://github.com/codesenberg/bombardier) using realistic 1.5 KB JSON webhook payloads (containing customer IDs, line items, timestamps, and nested metadata).

### Ingestion Performance Matrix (1,000 Requests / Tier)

| Concurrency | Ingest Throughput | Avg Latency | p50 Latency | p90 Latency | p95 Latency | p99 Latency | Error Rate |
| :---: | :---: | :---: | :---: | :---: | :---: | :---: | :---: |
| **10** | **848 req/s** | 11.4 ms | 10.1 ms | 10.8 ms | 37.8 ms | 38.8 ms | **0.00%** |
| **50** | **6,280 req/s** | 7.4 ms | **4.1 ms** | 8.5 ms | 33.8 ms | 43.0 ms | **0.00%** |
| **100** | **8,708 req/s** | 12.2 ms | **5.8 ms** | 36.4 ms | 41.8 ms | **42.8 ms** | **0.00%** |

---

## 2. Sustained 10-Second Saturation Run

To test memory stability, garbage collection pauses, and CPU efficiency under sustained saturation, Simple Relay was bombarded for 10 seconds continuously at 100 concurrent connections:

- **Total Requests Handled:** **100,622 requests**
- **Average Ingest Throughput:** **10,058 req/s**
- **Median Latency (p50):** **5.65 ms**
- **p95 Latency:** **35.81 ms**
- **p99 Latency:** **39.73 ms**
- **Failed Requests:** **0** (100% durable persistence confirmed)

---

## 3. Resource Footprint & RAM Profile

Measurements sampled directly via `docker stats` during idle, peak load, and post-load background drain phases:

| State | CPU Usage | RAM Usage | RAM % of Host | Description |
| :--- | :---: | :---: | :---: | :--- |
| **Idle** | `< 0.5%` | **4.2 MiB** | `~0.01%` | Baseline idle memory |
| **Peak Load (10,000 RPS)** | `~145%` | **24.0 – 26.1 MiB** | `~0.04%` | Sustained 10k req/s using ~1.45 CPU cores |
| **Post-Load (Drain Phase)** | `~2.5%` | **21.1 MiB** | `~0.03%` | Asynchronous worker pool processing |

> **Key Takeaway:** Even under a sustained bombardment of 10,000 requests per second, Simple Relay stays well under **26 MB of RAM**, making it viable to deploy on the smallest $5/month cloud instances without memory pressure.

---

## 4. How to Reproduce

You can reproduce these benchmarks on your own machine using the test runner in the Simple Relay repository:

```bash
git clone https://github.com/simple-relay/simple-relay.git
cd simple-relay

# Run automated container benchmark
./scripts/benchmark.sh

# Run custom volume test (5,000 requests, 100 concurrency)
NUM_REQUESTS=5000 CONCURRENCY_LIST="10 50 100" ./scripts/benchmark.sh
```
