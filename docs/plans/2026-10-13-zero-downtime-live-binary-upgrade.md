# Zero-Downtime Live Binary Upgrade Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement native zero-downtime live binary upgrades and socket handoff via POSIX file descriptor passing and graceful process teardown for standalone Simple Relay instances.

**Architecture:** A dedicated `internal/upgrade` package abstracts socket inheritance (`net.FileListener`), child process execution with duplicated socket FDs (`cmd.ExtraFiles`), anonymous OS pipe readiness signaling, systemd `sd_notify` PID handoff, and Windows fallback. `cmd/relay/main.go` and engine components are updated to serve on the managed listener, signal readiness upon boot, coordinate background dispatcher pausing during handover, and drain in-flight requests cleanly before exit.

**Tech Stack:** Go 1.22+, standard library `net`, `net/http`, `os`, `os/exec`, `syscall`, Go build tags (`//go:build !windows` vs `//go:build windows`), pure Go SQLite (`modernc.org/sqlite`).

**Spec:** `docs/specs/2026-10-12-zero-downtime-live-binary-upgrade-design.md`

## Global Constraints

- Pure Go standard library; zero external CGO dependencies (`CGO_ENABLED=0`).
- Durability Golden Rule: in-flight raw request payloads must be flushed by the parent's `BatchWriter` to SQLite WAL before parent exits.
- No dropped TCP connections during binary replacement on POSIX (Linux and macOS).
- Fail-fast rollback: if child fails to boot or signal readiness within 15s (`RELAY_UPGRADE_TIMEOUT`), parent must cancel the child and continue serving traffic uninterrupted.
- Systemd compatibility: if `$NOTIFY_SOCKET` is present, notify systemd with `MAINPID=<child_pid>` before parent exits.
- Cross-platform compile safety: clean compile on Linux, macOS, and Windows.

## Review Focus

1. **Linux deleted executable path resolution:** When binary is replaced on disk, `/proc/self/exe` appends ` (deleted)`. Resolver must trim this and verify the path exists.
2. **Readiness pipe timeout and hang:** If child process hangs during boot (e.g., blocked on SQLite locks), parent must not block forever; timeout after 15s, kill child, and resume.
3. **Double-dispatch prevention during transition:** Child process must suppress immediate `RecoverStuckRequests(time.Now())` on inherited boot; parent must stop its dispatcher polling loop immediately upon child readiness.
4. **Multiple consecutive SIGUSR2 signals:** Upgrade manager must reject or debounce overlapping upgrade requests while a handoff is already in progress.
5. **Windows graceful fallback:** On Windows, server must not crash or fail compilation; signal listener must cleanly report that live upgrade is unsupported.

---

### Task 1: Listener Inheritance & Readiness Protocol (`internal/upgrade`)

**Files:**
- Create: `internal/upgrade/listener.go`
- Create: `internal/upgrade/listener_test.go`

**Interfaces:**
- Produces:
  - `GetListener(addr string) (net.Listener, bool, error)`
  - `SignalReady() error`
  - Constants: `EnvInheritFD = "RELAY_INHERIT_FD"`, `EnvReadyFD = "RELAY_READY_FD"`, `EnvParentPID = "RELAY_PARENT_PID"`

- [ ] **Step 1: Write the failing unit tests for listener acquisition and readiness signaling**

Create `internal/upgrade/listener_test.go` testing:
1. `TestGetListener_ColdBoot`: calls `GetListener("127.0.0.1:0")`, asserts `inherited == false`, listener is active.
2. `TestGetListener_Inherited`: creates an existing listener, extracts FD via `.File()`, sets `RELAY_INHERIT_FD="3"`, calls `GetListener`, asserts `inherited == true`.
3. `TestSignalReady_NotInherited`: with empty env, returns `nil`.
4. `TestSignalReady_Inherited`: with pipe FD, writes `"1\n"` and verifies pipe receiver reads it.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/upgrade -run TestGetListener`
Expected: FAIL (package does not exist)

- [ ] **Step 3: Implement `internal/upgrade/listener.go`**

Implement `GetListener` and `SignalReady` handling environment variables, `os.NewFile`, and `net.FileListener`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/upgrade`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/upgrade/listener.go internal/upgrade/listener_test.go
git commit -m "feat(upgrade): implement socket inheritance and readiness protocol (#51)"
```

---

### Task 2: Executable Path Resolution & Upgraded Child Process Execution

**Files:**
- Create: `internal/upgrade/path.go`
- Create: `internal/upgrade/path_test.go`
- Create: `internal/upgrade/manager_posix.go`
- Create: `internal/upgrade/manager_windows.go`
- Create: `internal/upgrade/manager.go`
- Create: `internal/upgrade/manager_test.go`

**Interfaces:**
- Produces:
  - `ResolveExecutable() (string, error)`
  - `type UpgradeManager struct`
  - `NewUpgradeManager(ln net.Listener, onReady func(), logger *slog.Logger) *UpgradeManager`
  - `(m *UpgradeManager) TriggerUpgrade(ctx context.Context) error`
  - `(m *UpgradeManager) StartSignalListener(ctx context.Context)`

- [ ] **Step 1: Write failing tests for executable resolution and upgrade triggering**

Create `internal/upgrade/path_test.go` and `internal/upgrade/manager_test.go`:
1. `TestResolveExecutable_EnvOverride`: verify `RELAY_UPGRADE_BINARY` takes precedence.
2. `TestResolveExecutable_TrimDeletedSuffix`: verify `/bin/simple-relay (deleted)` trims to `/bin/simple-relay`.
3. `TestTriggerUpgrade_SuccessHandshake`: spawn a mock test subprocess that signals ready; verify parent receives confirmation within timeout.
4. `TestTriggerUpgrade_TimeoutAbort`: spawn a mock child that does not signal ready; verify parent kills child and returns error without crashing.

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/upgrade -run TestResolve`
Expected: FAIL

- [ ] **Step 3: Implement executable resolution, POSIX upgrade manager, and Windows fallback**

1. Implement `ResolveExecutable` in `internal/upgrade/path.go`.
2. Implement POSIX `UpgradeManager` in `internal/upgrade/manager_posix.go` (`//go:build !windows`):
   - Socket duplication via `listener.(*net.TCPListener).File()`.
   - Anonymous pipe via `os.Pipe()`.
   - Process launch with `cmd.ExtraFiles = []*os.File{lnFile, pipeWrite}`.
   - 15s timeout wait for pipe read.
   - `sd_notify` integration (`MAINPID=<pid>` sent to `$NOTIFY_SOCKET`).
   - Signal trapping for `syscall.SIGUSR2`.
3. Implement Windows fallback in `internal/upgrade/manager_windows.go` (`//go:build windows`).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -v ./internal/upgrade`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/upgrade/
git commit -m "feat(upgrade): implement upgrade manager and executable resolution (#51)"
```

---

### Task 3: Dispatcher & Recovery Coordination on Inherited Boot

**Files:**
- Modify: `internal/engine/dispatcher.go`
- Modify: `internal/engine/dispatcher_test.go`

**Interfaces:**
- Consumes: `upgrade.EnvInheritFD`
- Modifies: `DispatcherConfig` to accept `SkipImmediateRecovery bool`

- [ ] **Step 1: Write failing test in dispatcher for suppressed recovery on inherited boot**

In `internal/engine/dispatcher_test.go`, add `TestDispatcher_SkipImmediateRecovery`:
- Insert requests in `processing` state.
- Create Dispatcher with `SkipImmediateRecovery: true`.
- Start dispatcher; assert requests in `processing` are NOT immediately reset to `pending`.

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/engine -run TestDispatcher_SkipImmediateRecovery`
Expected: FAIL

- [ ] **Step 3: Implement `SkipImmediateRecovery` in `internal/engine/dispatcher.go`**

Update `Dispatcher.Start()` to skip `RecoverStuckRequests(ctx, time.Now().UTC())` when `cfg.SkipImmediateRecovery` is true.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/engine -run TestDispatcher_SkipImmediateRecovery`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/engine/dispatcher.go internal/engine/dispatcher_test.go
git commit -m "feat(engine): add SkipImmediateRecovery to dispatcher for live handoff (#51)"
```

---

### Task 4: Server Integration in `cmd/relay/main.go`

**Files:**
- Modify: `cmd/relay/main.go`

**Interfaces:**
- Integrates `upgrade.GetListener`, `upgrade.SignalReady`, `upgrade.NewUpgradeManager`, and `srv.Serve(ln)`

- [ ] **Step 1: Refactor `run` in `cmd/relay/main.go` to use managed listener and live reload**

1. Replace `srv.ListenAndServe()` with `ln, inherited, err := upgrade.GetListener(cfg.Address())`.
2. Configure `dispatcher` with `SkipImmediateRecovery: inherited`.
3. Instantiate `upgrade.NewUpgradeManager(ln, func() { dispatcher.Close() }, logger)`.
4. Launch `upgradeManager.StartSignalListener(ctx)`.
5. In server goroutine, call `srv.Serve(ln)`.
6. Once `srv.Serve` has begun accepting connections (or right after starting), call `upgrade.SignalReady()`.
7. Extend graceful shutdown drain timeout to `cfg.DrainTimeout` (default 30s) so active requests and batch writes finish cleanly.

- [ ] **Step 2: Verify `cmd/relay` compilation and unit tests**

Run: `go test -v ./cmd/relay`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add cmd/relay/main.go
git commit -m "feat(relay): integrate live upgrade manager and socket listener (#51)"
```

---

### Task 5: End-to-End Live Reload Verification & Cross-Platform Build Check

**Files:**
- Create: `cmd/relay/e2e_live_upgrade_test.go`

**Interfaces:**
- Full binary compilation, continuous HTTP traffic generation, `SIGUSR2` firing, zero dropped requests verification.

- [ ] **Step 1: Write E2E live reload integration test**

Create `cmd/relay/e2e_live_upgrade_test.go` (`//go:build !windows`):
1. Build `relay` binary to a temp directory.
2. Start binary on ephemeral port.
3. Fire 50 concurrent HTTP webhook requests per second in background goroutines.
4. Send `syscall.SIGUSR2` to running process.
5. Wait for parent process to exit with status 0.
6. Verify all HTTP requests during the window return 200/202 with zero connection failures.
7. Terminate child process cleanly.

- [ ] **Step 2: Run E2E test**

Run: `go test -v -run TestE2ELiveUpgrade ./cmd/relay`
Expected: PASS

- [ ] **Step 3: Run cross-platform build checks matching GoReleaser matrix**

Run:
```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /dev/null ./cmd/relay
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/relay
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o /dev/null ./cmd/relay
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o /dev/null ./cmd/relay
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o /dev/null ./cmd/relay
```
Expected: All exit with code 0.

- [ ] **Step 4: Commit**

```bash
git add cmd/relay/e2e_live_upgrade_test.go
git commit -m "test(relay): add end-to-end zero-downtime live upgrade verification test (#51)"
```
