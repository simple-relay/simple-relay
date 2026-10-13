//go:build !windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/simple-relay/simple-relay/internal/model"
	"github.com/simple-relay/simple-relay/internal/storage/sqlite"
	"github.com/simple-relay/simple-relay/internal/upgrade"
)

func TestE2ELiveUpgrade(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "relay")

	// 1. Compile fresh relay binary with CGO disabled for zero-dependency static execution
	goBin := "go"
	if p, err := exec.LookPath("go"); err == nil {
		goBin = p
	} else if home, err := os.UserHomeDir(); err == nil {
		fallback := filepath.Join(home, ".local", "go", "bin", "go")
		if _, err := os.Stat(fallback); err == nil {
			goBin = fallback
		}
	}

	buildTarget := "."
	if _, err := os.Stat("main.go"); err != nil {
		if _, err := os.Stat(filepath.Join("cmd", "relay", "main.go")); err == nil {
			buildTarget = "./cmd/relay"
		}
	}

	buildCmd := exec.Command(goBin, "build", "-o", binPath, buildTarget)
	buildCmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile relay binary: %v\nOutput: %s", err, string(out))
	}

	// 2. Setup mock upstream server and pre-seed inbound route in SQLite DB
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"received":true}`))
	}))
	defer mockUpstream.Close()

	dbPath := filepath.Join(tmpDir, "relay_e2e.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatalf("failed to run sqlite schema migration: %v", err)
	}

	routeRepo := sqlite.NewRouteRepo(db)
	testRoute := &model.Route{
		ID:                   model.NewULID(),
		Name:                 "e2e-webhook",
		Type:                 model.RouteTypeInbound,
		Mode:                 model.RouteModeAsync,
		Path:                 "/api/v1/webhook/e2e-webhook",
		DestinationURL:       mockUpstream.URL,
		SignatureProvider:    model.SignatureProviderNone,
		MaxAttempts:          3,
		TimeoutMS:            2000,
		RetryBackoffStrategy: model.RetryStrategyExponential,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}
	if err := routeRepo.CreateRoute(context.Background(), testRoute); err != nil {
		t.Fatalf("failed to pre-seed test route: %v", err)
	}
	_ = db.Close()

	// 3. Acquire ephemeral port
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate ephemeral port: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	logFilePath := filepath.Join(tmpDir, "relay.log")
	logFile, err := os.Create(logFilePath)
	if err != nil {
		t.Fatalf("failed to create server log file: %v", err)
	}
	defer logFile.Close()

	// 4. Start parent relay process on the ephemeral port
	parentCmd := exec.Command(binPath,
		"-host", "127.0.0.1",
		"-port", strconv.Itoa(port),
		"-db", dbPath,
		"-log-level", "debug",
		"-drain-timeout", "5s",
	)
	parentCmd.Stdout = logFile
	parentCmd.Stderr = logFile

	// Ensure parent process starts as a cold boot without inherited descriptors ⚡
	var parentEnv []string
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, upgrade.EnvInheritFD+"=") &&
			!strings.HasPrefix(env, upgrade.EnvReadyFD+"=") &&
			!strings.HasPrefix(env, upgrade.EnvParentPID+"=") {
			parentEnv = append(parentEnv, env)
		}
	}
	parentCmd.Env = parentEnv

	var childPID int
	t.Cleanup(func() {
		if parentCmd.Process != nil {
			_ = parentCmd.Process.Kill()
			_ = parentCmd.Wait()
		}
		if childPID > 0 {
			_ = syscall.Kill(childPID, syscall.SIGKILL)
		}
	})

	if err := parentCmd.Start(); err != nil {
		t.Fatalf("failed to start parent relay process: %v", err)
	}
	parentPID := parentCmd.Process.Pid

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	healthURL := baseURL + "/healthz"
	webhookURL := baseURL + "/api/v1/webhook/e2e-webhook"

	httpClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 50,
			IdleConnTimeout:     30 * time.Second,
		},
	}

	// Wait until parent process responds 200 OK on /healthz
	serverReady := false
	for i := 0; i < 50; i++ {
		resp, err := httpClient.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				serverReady = true
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !serverReady {
		logBytes, _ := os.ReadFile(logFilePath)
		t.Fatalf("parent process (PID %d) failed to become healthy\nLogs:\n%s", parentPID, string(logBytes))
	}

	// 5. Fire concurrent HTTP webhook requests at 50 req/s across background goroutines
	stopTraffic := make(chan struct{})
	var wg sync.WaitGroup

	var (
		totalRequests   atomic.Int64
		successRequests atomic.Int64
		failedRequests  atomic.Int64
		errListMu       sync.Mutex
		errList         []string
	)

	recordError := func(errMsg string) {
		failedRequests.Add(1)
		errListMu.Lock()
		if len(errList) < 50 {
			errList = append(errList, errMsg)
		}
		errListMu.Unlock()
	}

	workCh := make(chan int, 100)
	for w := 0; w < 50; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				select {
				case <-stopTraffic:
					return
				case seq, ok := <-workCh:
					if !ok {
						return
					}
					totalRequests.Add(1)
					payload := fmt.Sprintf(`{"worker":%d,"seq":%d,"ts":%d}`, workerID, seq, time.Now().UnixNano())
					req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, webhookURL, bytes.NewReader([]byte(payload)))
					if err != nil {
						recordError(fmt.Sprintf("request creation failure: %v", err))
						continue
					}
					req.Header.Set("Content-Type", "application/json")

					resp, err := httpClient.Do(req)
					if err != nil {
						recordError(fmt.Sprintf("connection/HTTP error: %v", err))
						continue
					}
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()

					if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted {
						successRequests.Add(1)
					} else {
						recordError(fmt.Sprintf("unexpected HTTP status: %d", resp.StatusCode))
					}
				}
			}
		}(w)
	}

	// Ticker dispatches 50 requests per second (1 every 20ms)
	var tickerWg sync.WaitGroup
	ticker := time.NewTicker(20 * time.Millisecond)
	tickerWg.Add(1)
	go func() {
		defer tickerWg.Done()
		defer ticker.Stop()
		seq := 0
		for {
			select {
			case <-stopTraffic:
				return
			case <-ticker.C:
				seq++
				select {
				case workCh <- seq:
				case <-stopTraffic:
					return
				}
			}
		}
	}()

	// Establish traffic baseline before triggering live reload
	time.Sleep(500 * time.Millisecond)

	// 6. Send SIGUSR2 to parent process to trigger live binary upgrade
	if err := syscall.Kill(parentPID, syscall.SIGUSR2); err != nil {
		t.Fatalf("failed to send SIGUSR2 to parent process (PID %d): %v", parentPID, err)
	}

	// 7. Wait for parent process to exit with status 0 (gracefully drained)
	parentExitCh := make(chan error, 1)
	go func() {
		parentExitCh <- parentCmd.Wait()
	}()

	select {
	case err := <-parentExitCh:
		if err != nil {
			logBytes, _ := os.ReadFile(logFilePath)
			t.Fatalf("parent process exited with error: %v\nLogs:\n%s", err, string(logBytes))
		}
	case <-time.After(15 * time.Second):
		logBytes, _ := os.ReadFile(logFilePath)
		t.Fatalf("parent process timed out waiting for graceful shutdown\nLogs:\n%s", string(logBytes))
	}

	// 8. Continue traffic against child process for 1 second to confirm seamless handover
	time.Sleep(1 * time.Second)

	// Stop ticker first and wait for it to exit before closing workCh ⚡
	close(stopTraffic)
	tickerWg.Wait()
	close(workCh)
	wg.Wait()

	t.Logf("Traffic summary: total=%d, successes=%d, failures=%d",
		totalRequests.Load(), successRequests.Load(), failedRequests.Load())

	// 9. Verify zero connection drops and 100% 200/202 responses during the upgrade window
	if failedRequests.Load() > 0 {
		errListMu.Lock()
		t.Fatalf("detected %d dropped or failed requests during live binary upgrade:\n%s",
			failedRequests.Load(), strings.Join(errList, "\n"))
	}
	if totalRequests.Load() < 50 {
		t.Fatalf("expected >= 50 requests dispatched, got %d", totalRequests.Load())
	}
	if successRequests.Load() != totalRequests.Load() {
		t.Fatalf("expected all %d requests to succeed, got %d",
			totalRequests.Load(), successRequests.Load())
	}

	// 10. Extract child PID from parent upgrade logs and verify child is alive and serving
	logBytes, err := os.ReadFile(logFilePath)
	if err != nil {
		t.Fatalf("failed to read server log file: %v", err)
	}

	re := regexp.MustCompile(`"child_pid":\s*(\d+)`)
	matches := re.FindAllSubmatch(logBytes, -1)
	if len(matches) == 0 {
		t.Fatalf("could not locate child_pid in logs:\n%s", string(logBytes))
	}
	lastMatch := matches[len(matches)-1]
	childPID, err = strconv.Atoi(string(lastMatch[1]))
	if err != nil || childPID <= 0 {
		t.Fatalf("invalid child PID parsed: %v (match: %s)", err, string(lastMatch[1]))
	}
	if childPID == parentPID {
		t.Fatalf("child PID (%d) unexpectedly matches parent PID (%d)", childPID, parentPID)
	}

	// Query /healthz to prove child is serving on the inherited socket
	resp, err := httpClient.Get(healthURL)
	if err != nil {
		t.Fatalf("failed to query /healthz on child process: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("child /healthz returned status %d", resp.StatusCode)
	}

	// 11. Terminate child process cleanly via SIGTERM and wait for clean exit
	if err := syscall.Kill(childPID, syscall.SIGTERM); err != nil {
		t.Fatalf("failed to send SIGTERM to child process %d: %v", childPID, err)
	}

	childTerminated := false
	for i := 0; i < 50; i++ {
		err := syscall.Kill(childPID, 0)
		if err != nil && errors.Is(err, syscall.ESRCH) {
			childTerminated = true
			break
		}
		// On Linux, check /proc/<pid>/status to catch reaped or zombie processes ⚡
		if statusBytes, procErr := os.ReadFile(fmt.Sprintf("/proc/%d/status", childPID)); procErr == nil {
			if strings.Contains(string(statusBytes), "State:\tZ (zombie)") {
				childTerminated = true
				break
			}
		} else if errors.Is(procErr, os.ErrNotExist) {
			childTerminated = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !childTerminated {
		_ = syscall.Kill(childPID, syscall.SIGKILL)
		t.Fatalf("child process %d failed to terminate cleanly within timeout", childPID)
	}
}
