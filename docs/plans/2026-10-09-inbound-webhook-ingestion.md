# Inbound Webhook Ingestion Implementation Plan

> **For agentic workers:** Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the durable inbound webhook ingestion layer (`POST /api/v1/webhook/{route_name}`) with raw byte preservation, single-writer SQLite WAL micro-batch persistence, and routing integration.

**Architecture:** A dedicated HTTP handler (`internal/api/handler_webhook.go`) validates inbound routes, accepts POST/PUT/PATCH, streams raw unmodified body bytes using `http.MaxBytesReader`, enqueues requests to `BatchWriter` for WAL durability before returning HTTP 200 OK (`{"relay_id": "...", "status": "persisted"}`), and delegates background delivery to the existing `Dispatcher` and `WorkerPool`.

**Tech Stack:** Go 1.22+, standard library `net/http` ServeMux, pure-Go SQLite (`modernc.org/sqlite`), `log/slog`, ULID.

## Global Constraints
- Pure Go, zero CGO (`CGO_ENABLED=0`).
- Durability Golden Rule: Return 200 OK only after committing to SQLite WAL.
- Zero payload mutation: capture and store raw byte slice in `requests.body`.
- Enforce max body size (`maxBodyBytes`, default 10MB).
- Hop-by-hop headers stripped; custom headers and payload bytes 100% preserved.

---

### Task 1: Webhook Handler & Unit Tests
- Create `internal/api/handler_webhook_test.go` with failing tests for:
  - Valid POST/PUT/PATCH webhook ingestion returning 200 OK with `relay_id` and `persisted`.
  - Route not found returning 404 Not Found.
  - Outbound route type returning 400 Bad Request.
  - Method not allowed (GET, DELETE) returning 405 Method Not Allowed.
  - Oversized payload returning 413 Request Entity Too Large.
  - Zero mutation verification of raw body and header preservation.
- Create `internal/api/handler_webhook.go` implementing `NewWebhookHandler`.
- Verify unit tests pass with `go test -v ./internal/api -run TestWebhookHandler`.

### Task 2: Router Integration & End-to-End Verification
- Update `internal/api/router.go` to register `/api/v1/webhook/{route_name}`.
- Create `internal/api/e2e_inbound_webhook_test.go`:
  - End-to-end integration test with live SQLite, BatchWriter, Dispatcher, WorkerPool, and mock destination server.
- Verify entire test suite with `go test -race ./...`.
